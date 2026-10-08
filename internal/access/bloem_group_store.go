package access

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrManagedGroup reports a generic mutation against a group owned by the
	// entitlement materializer. Managed groups change only through entitlement
	// operations so their source metadata and effective policy cannot diverge.
	ErrManagedGroup = errors.New("managed access groups can only be changed through entitlement policy operations")
)

// GroupDeletionImpact reports the canonical reassignment performed while a
// non-default group is deleted.
type GroupDeletionImpact struct {
	ProfilesReassigned int   `json:"profiles_reassigned"`
	DefaultGroupID     int64 `json:"default_group_id"`
}

type groupQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// GetInTransaction loads one access group from a caller-owned transaction.
func (s *TenantGroupStore) GetInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, id int64) (*TenantGroup, error) {
	return getTenantGroup(ctx, tx, organizationID, id)
}

func getTenantGroup(ctx context.Context, querier groupQueryRower, organizationID uuid.UUID, id int64) (*TenantGroup, error) {
	group, err := scanTenantGroup(querier.QueryRow(ctx, `
		SELECT `+tenantGroupSelectColumns+`, COUNT(p.id)::int AS member_count
		FROM access_groups g
		LEFT JOIN user_profiles p
		  ON p.organization_id = g.organization_id
		 AND p.access_group_id = g.id
		WHERE g.organization_id = $1
		  AND g.id = $2
		GROUP BY g.id`, organizationID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading access group: %w", err)
	}
	return group, nil
}

// GetForAccount resolves the group currently assigned to an account. It is
// used by platform-wide nested account routes, where there is deliberately no
// organization selected in request context.
func (s *TenantGroupStore) GetForAccount(ctx context.Context, accountID int, id int64) (*TenantGroup, error) {
	return getTenantGroupForAccount(ctx, s.pool, accountID, id)
}

// GetForAccountInTransaction resolves an account group in a caller-owned transaction.
func (s *TenantGroupStore) GetForAccountInTransaction(ctx context.Context, tx pgx.Tx, accountID int, id int64) (*TenantGroup, error) {
	return getTenantGroupForAccount(ctx, tx, accountID, id)
}

func getTenantGroupForAccount(ctx context.Context, querier groupQueryRower, accountID int, id int64) (*TenantGroup, error) {
	group, err := scanTenantGroup(querier.QueryRow(ctx, `
		SELECT `+tenantGroupSelectColumns+`, COUNT(p.id)::int AS member_count
		FROM access_groups g
		LEFT JOIN user_profiles p
		  ON p.organization_id = g.organization_id
		 AND p.access_group_id = g.id
		WHERE g.id = $2
		  AND EXISTS (
			SELECT 1 FROM organization_memberships m
			WHERE m.account_id = $1
			  AND m.organization_id = g.organization_id
			  AND m.access_group_id = g.id
		  )
		GROUP BY g.id`, accountID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading account access group: %w", err)
	}
	return group, nil
}

// GetDefault returns the group inherited by a new profile in an organization.
func (s *TenantGroupStore) GetDefault(ctx context.Context, organizationID uuid.UUID) (*TenantGroup, error) {
	return getDefaultTenantGroup(ctx, s.pool, organizationID)
}

// GetDefaultInTransaction resolves the default group in a caller-owned transaction.
func (s *TenantGroupStore) GetDefaultInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) (*TenantGroup, error) {
	return getDefaultTenantGroup(ctx, tx, organizationID)
}

func getDefaultTenantGroup(ctx context.Context, querier groupQueryRower, organizationID uuid.UUID) (*TenantGroup, error) {
	group, err := scanTenantGroup(querier.QueryRow(ctx, `
		SELECT `+tenantGroupSelectColumns+`, COUNT(p.id)::int AS member_count
		FROM access_groups g
		LEFT JOIN user_profiles p
		  ON p.organization_id = g.organization_id
		 AND p.access_group_id = g.id
		WHERE g.organization_id = $1 AND g.is_default
		GROUP BY g.id`, organizationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading default access group: %w", err)
	}
	return group, nil
}

func protectManagedDefault(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, replacementID int64) error {
	var currentID int64
	var managedKey *string
	var managedCohortID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id,managed_template_key,managed_cohort_id FROM access_groups
		WHERE organization_id=$1 AND is_default
		FOR UPDATE`, organizationID).Scan(&currentID, &managedKey, &managedCohortID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("locking current default access group: %w", err)
	}
	if (managedKey != nil || managedCohortID != nil) && currentID != replacementID {
		return ErrManagedGroup
	}
	return nil
}

func (s *TenantGroupStore) DeleteWithImpact(ctx context.Context, organizationID uuid.UUID, id int64) (GroupDeletionImpact, error) {
	return s.deleteConditionalWithImpact(ctx, organizationID, id, GroupPrecondition{Any: true})
}

func (s *TenantGroupStore) deleteConditionalWithImpact(ctx context.Context, organizationID uuid.UUID, id int64, guard GroupPrecondition, onMoved ...func(context.Context, pgx.Tx, []int) error) (GroupDeletionImpact, error) {
	if !guard.valid() {
		return GroupDeletionImpact{}, ErrGroupInvalidPrecondition
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("beginning access group delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockTenantGroupWriters(ctx, tx); err != nil {
		return GroupDeletionImpact{}, err
	}
	if _, err := lockTenantGroup(ctx, tx, organizationID, id, guard); err != nil {
		return GroupDeletionImpact{}, err
	}
	var (
		isDefault          bool
		managedTemplateKey *string
		managedCohortID    *uuid.UUID
	)
	err = tx.QueryRow(ctx, `
		SELECT is_default, managed_template_key, managed_cohort_id
		FROM access_groups
		WHERE organization_id = $1
		  AND id = $2
		FOR UPDATE`, organizationID, id).Scan(&isDefault, &managedTemplateKey, &managedCohortID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return GroupDeletionImpact{}, ErrGroupNotFound
	case err != nil:
		return GroupDeletionImpact{}, fmt.Errorf("checking access group default flag: %w", err)
	case managedTemplateKey != nil || managedCohortID != nil:
		return GroupDeletionImpact{}, ErrManagedGroup
	case isDefault:
		return GroupDeletionImpact{}, ErrDefaultGroupRequired
	}
	var defaultGroupID int64
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM access_groups
		WHERE organization_id = $1
		  AND is_default
		FOR UPDATE`, organizationID).Scan(&defaultGroupID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GroupDeletionImpact{}, ErrDefaultGroupRequired
		}
		return GroupDeletionImpact{}, fmt.Errorf("loading replacement default access group: %w", err)
	}

	var moved []int
	if len(onMoved) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT account_id FROM organization_memberships
			WHERE organization_id = $1 AND access_group_id = $2
			UNION
			SELECT user_id FROM user_profiles
			WHERE organization_id = $1 AND access_group_id = $2`, organizationID, id)
		if err != nil {
			return GroupDeletionImpact{}, fmt.Errorf("loading reassigned accounts: %w", err)
		}
		moved, err = pgx.CollectRows(rows, pgx.RowTo[int])
		if err != nil {
			return GroupDeletionImpact{}, err
		}
	}

	if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		return GroupDeletionImpact{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET access_policy_revision = access_policy_revision + 1
		WHERE organization_id = $1
		  AND (access_group_id = $2 OR account_id IN (
			SELECT DISTINCT user_id
			FROM user_profiles
			WHERE organization_id = $1
			  AND access_group_id = $2
		))`, organizationID, id); err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("bumping deleted access group member revisions: %w", err)
	}
	profileTag, err := tx.Exec(ctx, `
		UPDATE user_profiles
		SET access_group_id = $3,
			updated_at = NOW()
		WHERE organization_id = $1
		  AND access_group_id = $2`, organizationID, id, defaultGroupID)
	if err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("reassigning deleted access group profiles: %w", err)
	}
	// Memberships hold access_group_id too, under
	// organization_memberships_organization_access_group_fkey ON DELETE RESTRICT.
	// Reassigning only the profiles leaves memberships pointing at the doomed
	// group and the DELETE below fails with a raw 23001.
	//
	// Upstream Silo declares users.access_group_id ON DELETE SET NULL, so a
	// delete there silently detaches members and drops them to "no group", which
	// reads downstream as no access-group restrictions at all. Reassigning to the
	// organization default instead is deliberate: deleting a group must never
	// silently widen what its members can reach. See
	// docs/architecture/multitenant-administration.md.
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET access_group_id = $3,
			updated_at = NOW()
		WHERE organization_id = $1
		  AND access_group_id = $2`, organizationID, id, defaultGroupID); err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("reassigning deleted access group memberships: %w", err)
	}
	for _, callback := range onMoved {
		if err := callback(ctx, tx, moved); err != nil {
			return GroupDeletionImpact{}, err
		}
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM access_groups
		WHERE organization_id = $1
		  AND id = $2`, organizationID, id)
	if err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("deleting access group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return GroupDeletionImpact{}, ErrGroupNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("committing access group delete: %w", err)
	}
	return GroupDeletionImpact{ProfilesReassigned: int(profileTag.RowsAffected()), DefaultGroupID: defaultGroupID}, nil
}

func groupAuthorizationChanged(current TenantGroup, input TenantUpdateGroupInput) bool {
	return input.LibraryIDs != nil && !reflect.DeepEqual(current.LibraryIDs, *input.LibraryIDs) ||
		input.MaxPlaybackQuality != nil && NormalizePlaybackQuality(current.MaxPlaybackQuality) != NormalizePlaybackQuality(*input.MaxPlaybackQuality) ||
		input.PlaybackAllowed != nil && current.PlaybackAllowed != *input.PlaybackAllowed ||
		input.DownloadAllowed != nil && current.DownloadAllowed != *input.DownloadAllowed ||
		input.DownloadTranscodeAllowed != nil && current.DownloadTranscodeAllowed != *input.DownloadTranscodeAllowed ||
		input.TranscodeAllowed != nil && current.TranscodeAllowed != *input.TranscodeAllowed ||
		input.AudioTranscodeAllowed != nil && current.AudioTranscodeAllowed != *input.AudioTranscodeAllowed ||
		input.MaxStreams != nil && current.MaxStreams != *input.MaxStreams ||
		input.MaxProfiles != nil && current.MaxProfiles != *input.MaxProfiles ||
		input.MaxTranscodes != nil && current.MaxTranscodes != *input.MaxTranscodes ||
		input.MaxRemoteStreamBitrateKbps != nil && current.MaxRemoteStreamBitrateKbps != *input.MaxRemoteStreamBitrateKbps ||
		input.MaxLocalStreamBitrateKbps != nil && current.MaxLocalStreamBitrateKbps != *input.MaxLocalStreamBitrateKbps ||
		input.AllowedPermissions != nil && !reflect.DeepEqual(current.AllowedPermissions, *input.AllowedPermissions) ||
		input.RequestsAllowed != nil && current.RequestsAllowed != *input.RequestsAllowed
}

// ResolvePolicy returns the profile's organization-owned group policy. The
// legacy account-level assignment is available only for a profile-less request
// in the default organization during the compatibility window.
func (s *TenantGroupStore) ResolvePolicy(ctx context.Context, subject GroupSubject) (*GroupPolicy, error) {
	return resolveGroupPolicy(ctx, s.pool, subject)
}

// GetPolicyForUser satisfies Silo's provider interface from the request
// tenant; it fails closed without one.
func (s *TenantGroupStore) GetPolicyForUser(ctx context.Context, userID int) (*GroupPolicy, error) {
	subject, err := GroupSubjectFromContext(ctx, userID, "")
	if err != nil {
		return nil, err
	}
	return s.ResolvePolicy(ctx, subject)
}

func resolveGroupPolicy(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, subject GroupSubject) (*GroupPolicy, error) {
	if subject.OrganizationID == uuid.Nil || subject.AccountID <= 0 {
		return nil, ErrGroupNotFound
	}
	if subject.ProfileID != "" {
		return nullableGroupPolicy(db.QueryRow(ctx, `
			SELECT p.access_group_id, g.id, g.library_ids, g.max_playback_quality,
				g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
				g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
				g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps, g.allowed_permissions, g.requests_allowed
			FROM user_profiles p
			LEFT JOIN access_groups g
			  ON g.organization_id = p.organization_id
			 AND g.id = p.access_group_id
			WHERE p.organization_id = $1
			  AND p.user_id = $2
			  AND p.id = $3`, subject.OrganizationID, subject.AccountID, subject.ProfileID))
	}
	if !subject.Legacy {
		return nil, ErrGroupNotFound
	}
	return nullableGroupPolicy(db.QueryRow(ctx, `
		SELECT u.access_group_id, g.id, g.library_ids, g.max_playback_quality,
			g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
			g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
			g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps, g.allowed_permissions, g.requests_allowed
		FROM organization_memberships u
		JOIN organizations o
		  ON o.id = $1
		 AND o.is_default
		 AND o.id = u.organization_id
		LEFT JOIN access_groups g
		  ON g.organization_id = o.id
		 AND g.id = u.access_group_id
		WHERE u.account_id = $2`, subject.OrganizationID, subject.AccountID))
}

func nullableGroupPolicy(row groupScanner) (*GroupPolicy, error) {
	var (
		assignedGroupID  *int64
		groupID          *int64
		libraryIDs       []int
		maxQuality       *string
		playbackAllowed  *bool
		downloadAllowed  *bool
		downloadTx       *bool
		transcodeTx      *bool
		audioTx          *bool
		maxStreams       *int
		maxProfiles      *int
		maxTranscodes    *int
		maxRemoteBitrate *int
		maxLocalBitrate  *int
		permissions      []string
		requestsAllowed  *bool
	)
	if err := row.Scan(
		&assignedGroupID,
		&groupID,
		&libraryIDs,
		&maxQuality,
		&playbackAllowed,
		&downloadAllowed,
		&downloadTx,
		&transcodeTx,
		&audioTx,
		&maxStreams,
		&maxProfiles,
		&maxTranscodes,
		&maxRemoteBitrate,
		&maxLocalBitrate,
		&permissions,
		&requestsAllowed,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("loading access group policy: %w", err)
	}
	if assignedGroupID == nil {
		return nil, nil
	}
	if groupID == nil || maxQuality == nil || playbackAllowed == nil || downloadAllowed == nil || downloadTx == nil || transcodeTx == nil || audioTx == nil || maxStreams == nil || maxProfiles == nil || maxTranscodes == nil || maxRemoteBitrate == nil || maxLocalBitrate == nil || requestsAllowed == nil {
		return nil, ErrGroupNotFound
	}
	return &GroupPolicy{
		ID:                         *groupID,
		LibraryIDs:                 libraryIDs,
		MaxPlaybackQuality:         *maxQuality,
		PlaybackAllowed:            *playbackAllowed,
		DownloadAllowed:            *downloadAllowed,
		DownloadTranscodeAllowed:   *downloadTx,
		TranscodeAllowed:           *transcodeTx,
		AudioTranscodeAllowed:      *audioTx,
		MaxStreams:                 *maxStreams,
		MaxProfiles:                *maxProfiles,
		MaxTranscodes:              *maxTranscodes,
		MaxRemoteStreamBitrateKbps: *maxRemoteBitrate,
		MaxLocalStreamBitrateKbps:  *maxLocalBitrate,
		AllowedPermissions:         permissions,
		RequestsAllowed:            *requestsAllowed,
	}, nil
}

// ReadAccountGroupInTransaction reads only a group assigned to this account's membership.
func ReadAccountGroupInTransaction(ctx context.Context, tx pgx.Tx, accountID int, id int64) (*TenantGroup, error) {
	return getTenantGroupForAccount(ctx, tx, accountID, id)
}

// DeleteMovingMembers keeps active sign-ins while
// reassigning both profiles and memberships within the selected organization.
func (s *TenantGroupStore) DeleteMovingMembers(ctx context.Context, organizationID uuid.UUID, id int64, guard GroupPrecondition, onMoved func(context.Context, pgx.Tx, []int) error) ([]int, error) {
	var moved []int
	_, err := s.deleteConditionalWithImpact(ctx, organizationID, id, guard, func(ctx context.Context, tx pgx.Tx, ids []int) error {
		moved = ids
		if onMoved != nil && len(ids) > 0 {
			return onMoved(ctx, tx, ids)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}

// TenantGroupStore is the organization-qualified repository used by server callers.
type TenantGroupStore struct{ pool *pgxpool.Pool }

func NewTenantGroupStore(pool *pgxpool.Pool) *TenantGroupStore { return &TenantGroupStore{pool: pool} }

const tenantGroupSelectColumns = `g.id, g.organization_id, g.name, g.description, g.library_ids, g.max_playback_quality,
	g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
	g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
	g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps, g.allowed_permissions, g.requests_allowed, g.is_default,
	g.managed_template_key, g.managed_template_revision, g.managed_cohort_id, g.created_at, g.updated_at, g.configuration_revision`

type tenantGroupScanner interface {
	Scan(dest ...any) error
}

func scanTenantGroup(row tenantGroupScanner) (*TenantGroup, error) {
	var g TenantGroup
	var managedCohortID *uuid.UUID
	if err := row.Scan(
		&g.ID,
		&g.OrganizationID,
		&g.Name,
		&g.Description,
		&g.LibraryIDs,
		&g.MaxPlaybackQuality,
		&g.PlaybackAllowed,
		&g.DownloadAllowed,
		&g.DownloadTranscodeAllowed,
		&g.TranscodeAllowed,
		&g.AudioTranscodeAllowed,
		&g.MaxStreams,
		&g.MaxProfiles,
		&g.MaxTranscodes,
		&g.MaxRemoteStreamBitrateKbps,
		&g.MaxLocalStreamBitrateKbps,
		&g.AllowedPermissions,
		&g.RequestsAllowed,
		&g.IsDefault,
		&g.ManagedTemplateKey,
		&g.ManagedTemplateRevision,
		&managedCohortID,
		&g.CreatedAt,
		&g.UpdatedAt,
		&g.Revision,
		&g.MemberCount,
	); err != nil {
		return nil, err
	}
	if managedCohortID != nil {
		g.ManagedCohortID = *managedCohortID
	}
	return &g, nil
}

// List returns all access groups in an organization with profile member counts.
func (s *TenantGroupStore) List(ctx context.Context, organizationID uuid.UUID) ([]TenantGroup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+tenantGroupSelectColumns+`, COUNT(p.id)::int AS member_count
		FROM access_groups g
		LEFT JOIN user_profiles p
		  ON p.organization_id = g.organization_id
		 AND p.access_group_id = g.id
		WHERE g.organization_id = $1
		GROUP BY g.id
		ORDER BY lower(g.name), g.id`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("listing access groups: %w", err)
	}
	defer rows.Close()

	groups := []TenantGroup{}
	for rows.Next() {
		group, err := scanTenantGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning access group: %w", err)
		}
		groups = append(groups, *group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating access groups: %w", err)
	}
	return groups, nil
}

// Get returns one access group with its member count.
func (s *TenantGroupStore) Get(ctx context.Context, organizationID uuid.UUID, id int64) (*TenantGroup, error) {
	return getTenantGroup(ctx, s.pool, organizationID, id)
}

// Serialize the small administrator-maintained configuration set before row
// locks. Default promotion edits a sibling, so target-first locks can deadlock.
func lockTenantGroupWriters(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `LOCK TABLE access_groups IN SHARE ROW EXCLUSIVE MODE`)
	return err
}

// ReadTenantGroupInTransaction reads group configuration using the caller's existing transaction.
// Callers coordinating user assignment must acquire their group lock before user locks.
func ReadTenantGroupInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, id int64) (*TenantGroup, error) {
	return getTenantGroup(ctx, tx, organizationID, id)
}
func lockTenantGroup(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, id int64, guard GroupPrecondition) (*TenantGroup, error) {
	if _, err := tx.Exec(ctx, `SELECT id FROM access_groups WHERE organization_id=$1 AND id=$2 FOR UPDATE`, organizationID, id); err != nil {
		return nil, err
	}
	g, err := ReadTenantGroupInTransaction(ctx, tx, organizationID, id)
	if err != nil {
		return nil, err
	}
	if !guard.Any && guard.Revision != g.Revision {
		return nil, &GroupRevisionConflict{Current: &g.Group}
	}
	return g, nil
}

func (s *TenantGroupStore) ListPage(ctx context.Context, organizationID uuid.UUID, after *GroupPageKey, limit int) ([]TenantGroup, bool, error) {
	if limit < 1 {
		limit = 50
	}
	limit = min(limit, 200)
	var id int64
	if after != nil {
		id = after.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT `+tenantGroupSelectColumns+`,(SELECT count(*)::int FROM user_profiles p WHERE p.organization_id=g.organization_id AND p.access_group_id=g.id) FROM access_groups g WHERE g.organization_id=$1 AND g.id>$2 ORDER BY g.id LIMIT $3`, organizationID, id, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []TenantGroup{}
	for rows.Next() {
		g, err := scanTenantGroup(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *g)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// TenantGroupPolicyInTransaction reads the exact tenant/profile authority in the
// caller's snapshot. An account's legacy group is not a substitute for a
// profile's group. The subject must agree with validated request tenancy.
func TenantGroupPolicyInTransaction(ctx context.Context, tx pgx.Tx, subject GroupSubject) (*GroupPolicy, error) {
	validated, err := GroupSubjectFromContext(ctx, subject.AccountID, subject.ProfileID)
	if err != nil || validated != subject {
		return nil, ErrGroupNotFound
	}
	return resolveGroupPolicy(ctx, tx, subject)
}

// bloemAccountGroupPolicy backs Silo's account-level GroupStore on Bloem's
// schema, where users.access_group_id no longer exists and groups are
// organization-owned. It reads the account's membership group in the request
// tenant when ctx carries one for this account, otherwise in the default
// organization (the legacy account-level compatibility path). An account with
// no such membership or no assigned group has no group, matching Silo's
// "nil when the user has no group". Server wiring uses TenantGroupStore,
// whose profile-scoped, fail-closed resolution this does not change.
func bloemAccountGroupPolicy(ctx context.Context, db groupQueryRower, accountID int) (*GroupPolicy, error) {
	var organizationID *uuid.UUID
	if tenant, ok := tenancy.FromContext(ctx); ok && tenant.AccountID == accountID && tenant.OrganizationID != uuid.Nil {
		organizationID = &tenant.OrganizationID
	}
	policy, err := nullableGroupPolicy(db.QueryRow(ctx, `
		SELECT m.access_group_id, g.id, g.library_ids, g.max_playback_quality,
			g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
			g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
			g.max_transcodes, g.max_remote_stream_bitrate_kbps, g.max_local_stream_bitrate_kbps, g.allowed_permissions, g.requests_allowed
		FROM organization_memberships m
		JOIN organizations o
		  ON o.id = m.organization_id
		 AND (o.id = $2::uuid OR ($2::uuid IS NULL AND o.is_default))
		LEFT JOIN access_groups g
		  ON g.organization_id = o.id
		 AND g.id = m.access_group_id
		WHERE m.account_id = $1`, accountID, organizationID))
	if errors.Is(err, ErrGroupNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading access group policy for user %d: %w", accountID, err)
	}
	return policy, nil
}
