package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// bloemWiderThan adds Bloem's two policy fields to WiderThan: a policy that
// may play where the limit may not, and a full policy where the limit is
// preview-only (accesspolicy.PreviewTemplateKey), each grant more.
func bloemWiderThan(policy, limit EffectiveUserPolicy) []PolicyWidening {
	var out []PolicyWidening
	if policy.PlaybackAllowed && !limit.PlaybackAllowed {
		out = append(out, PolicyWidening{Field: "playback_allowed", Policy: "allowed", Limit: "not allowed"})
	}
	if !policy.PreviewOnly && limit.PreviewOnly {
		out = append(out, PolicyWidening{Field: "preview_only", Policy: "full access", Limit: "preview only"})
	}
	return out
}

// bloemGroupPolicyColumns are scanGroupPolicy's columns, then the group's
// playback switch and whether it is the preview plan's group, which Silo's
// scan does not read.
const bloemGroupPolicyColumns = `g.id, g.library_ids, g.max_playback_quality, g.download_allowed,
	g.download_transcode_allowed, g.transcode_allowed, g.audio_transcode_allowed,
	g.max_streams, g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps,
	g.max_profiles, g.allowed_permissions, g.requests_allowed,
	g.playback_allowed, g.managed_template_key IS NOT DISTINCT FROM 'bloem-preview'`

func bloemScanGroupPolicy(row groupScanner) (*GroupPolicy, error) {
	var p GroupPolicy
	if err := row.Scan(&p.ID, &p.LibraryIDs, &p.MaxPlaybackQuality, &p.DownloadAllowed, &p.DownloadTranscodeAllowed,
		&p.TranscodeAllowed, &p.AudioTranscodeAllowed, &p.MaxStreams, &p.MaxTranscodes, &p.MaxRemoteStreamBitrateKbps,
		&p.MaxLocalStreamBitrateKbps, &p.MaxProfiles, &p.AllowedPermissions, &p.RequestsAllowed,
		&p.PlaybackAllowed, &p.PreviewOnly); err != nil {
		return nil, err
	}
	return &p, nil
}

// BloemGroupPolicyByID reads one group's policy with its playback switch and
// preview flag; nil when there is no such group.
func BloemGroupPolicyByID(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (*GroupPolicy, error) {
	policy, err := bloemScanGroupPolicy(db.QueryRow(ctx, `SELECT `+bloemGroupPolicyColumns+` FROM access_groups g WHERE g.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading access group %d: %w", id, err)
	}
	return policy, nil
}
