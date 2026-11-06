package backup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
)

// Bloem: a restore never gives an account or a profile more than the backup
// did. Silo compares an account's resolved policy field by field
// (access.WiderThan); Bloem adds its playback switch and preview flag to that
// comparison (bloemWiderThan) and to the restore set, and compares each new
// profile's group, since in Bloem a profile's own group decides what it may
// do. A restore set written before these fields compares without them.

// bloemGroupAccess is g.access() with Bloem's playback switch and preview
// flag. A group recorded without the switch could play.
func bloemGroupAccess(g GroupPolicy) access.GroupPolicy {
	p := g.access()
	p.PlaybackAllowed = g.BloemPlaybackAllowed == nil || *g.BloemPlaybackAllowed
	p.PreviewOnly = g.BloemPreviewOnly
	return p
}

// bloemAttachAccess records, in each account entry, its group's playback
// switch and preview flag and its resolved policy's, which Silo's account
// read leaves out.
func bloemAttachAccess(ctx context.Context, tx pgx.Tx, accounts []AccountEntry) error {
	rows, err := tx.Query(ctx, `
		SELECT u.id, g.playback_allowed, g.managed_template_key IS NOT DISTINCT FROM 'bloem-preview'
		FROM users u JOIN access_groups g ON g.id = u.access_group_id`)
	if err != nil {
		return fmt.Errorf("reading account groups' playback: %w", err)
	}
	defer rows.Close()
	type flags struct{ playback, preview bool }
	byAccount := map[int]flags{}
	for rows.Next() {
		var id int
		var f flags
		if err := rows.Scan(&id, &f.playback, &f.preview); err != nil {
			return err
		}
		byAccount[id] = f
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range accounts {
		a := &accounts[i]
		var group *access.GroupPolicy
		if a.Group != nil {
			f := byAccount[a.ID]
			a.Group.Policy.BloemPlaybackAllowed, a.Group.Policy.BloemPreviewOnly = new(f.playback), f.preview
			g := bloemGroupAccess(a.Group.Policy)
			group = &g
		}
		effective := access.ApplyGroupPolicy(a.user(), group)
		a.Effective.BloemPlaybackAllowed, a.Effective.BloemPreviewOnly = new(effective.PlaybackAllowed), effective.PreviewOnly
	}
	return nil
}

// bloemLimitFields puts the backup's playback switch and preview flag into
// the limit a new account is compared with. An entry written without them
// sets none, so it compares as the backup's server could play.
func bloemLimitFields(limit *access.EffectiveUserPolicy, e EffectivePolicy) {
	limit.PlaybackAllowed = e.BloemPlaybackAllowed == nil || *e.BloemPlaybackAllowed
	limit.PreviewOnly = e.BloemPreviewOnly
}

// bloemProfileGroupColumns read a profile's group as GroupEntry carries it.
const bloemProfileGroupColumns = `g.name, g.library_ids, coalesce(g.max_playback_quality, ''), g.download_allowed,
	g.download_transcode_allowed, g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_transcodes,
	g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps, g.max_profiles, g.allowed_permissions,
	g.requests_allowed, g.playback_allowed, g.managed_template_key IS NOT DISTINCT FROM 'bloem-preview'`

func scanProfileGroup(rows pgx.Rows) (string, GroupEntry, error) {
	var id string
	var e GroupEntry
	p := &e.Policy
	var playback bool
	err := rows.Scan(&id, &e.Name, &p.LibraryIDs, &p.MaxPlaybackQuality, &p.DownloadAllowed, &p.DownloadTranscodeAllowed,
		&p.TranscodeAllowed, &p.AudioTranscodeAllowed, &p.MaxStreams, &p.MaxTranscodes, &p.MaxRemoteStreamBitrateKbps,
		&p.MaxLocalStreamBitrateKbps, &p.MaxProfiles, &p.AllowedPermissions, &p.RequestsAllowed, &playback, &p.BloemPreviewOnly)
	p.BloemPlaybackAllowed = new(playback)
	return id, e, err
}

// bloemCheckProfileGroup leaves a new profile out when the group it would
// join here (pgstore.BloemRestoredProfileGroup) gives it more than its group
// in the backup did.
func (u *userPlanner) bloemCheckProfileGroup(ctx context.Context, plan *accountPlan, pp *profilePlan) error {
	e := pp.entry
	if pp.localID != "" || !pp.preview.Selectable || e.BloemGroup == nil {
		return nil
	}
	organizationID, accountGroup, err := u.bloemRestoreTarget(ctx, plan)
	if err != nil || organizationID == "" {
		return err
	}
	groupID, err := pgstore.BloemRestoredProfileGroup(ctx, u.q, organizationID, accountGroup, e.BloemGroup.Name)
	if err != nil {
		return err
	}
	here, err := access.BloemGroupPolicyByID(ctx, u.q, groupID)
	if err != nil || here == nil {
		return err
	}
	user := plan.doc.Account.user()
	user.LibraryIDs, _ = mapLibraries(user.LibraryIDs, u.libMap)
	backup := bloemGroupAccess(e.BloemGroup.Policy)
	backup.LibraryIDs, _ = mapLibraries(backup.LibraryIDs, u.libMap)
	widenings := access.WiderThan(access.ApplyGroupPolicy(user, here), access.ApplyGroupPolicy(user, &backup))
	if len(widenings) == 0 {
		return nil
	}
	fields := make([]string, len(widenings))
	for i, w := range widenings {
		fields[i] = w.Field
	}
	pp.preview.Status, pp.preview.Selectable, pp.preview.Reason = ItemNotOnThisServer, false, ReasonPolicyWider
	pp.preview.Message = fmt.Sprintf("The access group it would join here gives it more than its group %q did in the backup: %s.",
		e.BloemGroup.Name, strings.Join(fields, ", "))
	return nil
}

// bloemRestoreTarget is the organization a planned account's profiles are
// created in and the account's group there: the membership of an account
// already here, or, for a new account, the organization it is restored into
// (bloemPlanContext) and the group it joins.
func (u *userPlanner) bloemRestoreTarget(ctx context.Context, plan *accountPlan) (string, *int64, error) {
	if plan.localID == 0 {
		organizationID, ok := tenancy.CreationOrganization(ctx)
		if !ok {
			return "", nil, nil
		}
		return organizationID.String(), plan.groupID, nil
	}
	var organizationID string
	var group *int64
	err := u.q.QueryRow(ctx, `
		SELECT m.organization_id::text,
			(SELECT g.id FROM access_groups g WHERE g.id = u.access_group_id AND g.organization_id = m.organization_id)
		FROM organization_memberships m JOIN users u ON u.id = m.account_id
		WHERE m.account_id = $1`, plan.localID).Scan(&organizationID, &group)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading the organization of account %d: %w", plan.localID, err)
	}
	return organizationID, group, nil
}
