package access

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
func (s *GroupStore) GetInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, id int64) (*Group, error) {
	return getGroup(ctx, tx, organizationID, id)
}

func getGroup(ctx context.Context, querier groupQueryRower, organizationID uuid.UUID, id int64) (*Group, error) {
	group, err := scanGroup(querier.QueryRow(ctx, `
		SELECT `+accessGroupSelectColumns+`, COUNT(p.id)::int AS member_count
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
func (s *GroupStore) GetForAccount(ctx context.Context, accountID int, id int64) (*Group, error) {
	return getGroupForAccount(ctx, s.pool, accountID, id)
}

// GetForAccountInTransaction resolves an account group in a caller-owned transaction.
func (s *GroupStore) GetForAccountInTransaction(ctx context.Context, tx pgx.Tx, accountID int, id int64) (*Group, error) {
	return getGroupForAccount(ctx, tx, accountID, id)
}

func getGroupForAccount(ctx context.Context, querier groupQueryRower, accountID int, id int64) (*Group, error) {
	group, err := scanGroup(querier.QueryRow(ctx, `
		SELECT `+accessGroupSelectColumns+`, COUNT(p.id)::int AS member_count
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
func (s *GroupStore) GetDefault(ctx context.Context, organizationID uuid.UUID) (*Group, error) {
	return getDefaultGroup(ctx, s.pool, organizationID)
}

// GetDefaultInTransaction resolves the default group in a caller-owned transaction.
func (s *GroupStore) GetDefaultInTransaction(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) (*Group, error) {
	return getDefaultGroup(ctx, tx, organizationID)
}

func getDefaultGroup(ctx context.Context, querier groupQueryRower, organizationID uuid.UUID) (*Group, error) {
	group, err := scanGroup(querier.QueryRow(ctx, `
		SELECT `+accessGroupSelectColumns+`, COUNT(p.id)::int AS member_count
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

func (s *GroupStore) DeleteWithImpact(ctx context.Context, organizationID uuid.UUID, id int64) (GroupDeletionImpact, error) {
	return s.deleteConditionalWithImpact(ctx, organizationID, id, GroupPrecondition{Any: true})
}

func (s *GroupStore) deleteConditionalWithImpact(ctx context.Context, organizationID uuid.UUID, id int64, guard GroupPrecondition) (GroupDeletionImpact, error) {
	if !guard.valid() {
		return GroupDeletionImpact{}, ErrGroupInvalidPrecondition
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("beginning access group delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockGroupWriters(ctx, tx); err != nil {
		return GroupDeletionImpact{}, err
	}
	if _, err := lockGroup(ctx, tx, organizationID, id, guard); err != nil {
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

	if err := tenancy.MarkMembershipPolicyWriter(ctx, tx); err != nil {
		return GroupDeletionImpact{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_memberships
		SET access_policy_revision = access_policy_revision + 1
		WHERE organization_id = $1
		  AND account_id IN (
			SELECT DISTINCT user_id
			FROM user_profiles
			WHERE organization_id = $1
			  AND access_group_id = $2
		)`, organizationID, id); err != nil {
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

func groupAuthorizationChanged(current Group, input UpdateGroupInput) bool {
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
		input.AllowedPermissions != nil && !reflect.DeepEqual(current.AllowedPermissions, *input.AllowedPermissions) ||
		input.RequestsAllowed != nil && current.RequestsAllowed != *input.RequestsAllowed
}

// ResolvePolicy returns the profile's organization-owned group policy. The
// legacy account-level assignment is available only for a profile-less request
// in the default organization during the compatibility window.
func (s *GroupStore) ResolvePolicy(ctx context.Context, subject GroupSubject) (*GroupPolicy, error) {
	return resolveGroupPolicy(ctx, s.pool, subject)
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
				g.max_transcodes, g.allowed_permissions, g.requests_allowed
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
			g.max_transcodes, g.allowed_permissions, g.requests_allowed
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
		assignedGroupID *int64
		groupID         *int64
		libraryIDs      []int
		maxQuality      *string
		playbackAllowed *bool
		downloadAllowed *bool
		downloadTx      *bool
		transcodeTx     *bool
		audioTx         *bool
		maxStreams      *int
		maxProfiles     *int
		maxTranscodes   *int
		permissions     []string
		requestsAllowed *bool
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
	if groupID == nil || maxQuality == nil || playbackAllowed == nil || downloadAllowed == nil || downloadTx == nil || transcodeTx == nil || audioTx == nil || maxStreams == nil || maxProfiles == nil || maxTranscodes == nil || requestsAllowed == nil {
		return nil, ErrGroupNotFound
	}
	return &GroupPolicy{
		ID:                       *groupID,
		LibraryIDs:               libraryIDs,
		MaxPlaybackQuality:       *maxQuality,
		PlaybackAllowed:          *playbackAllowed,
		DownloadAllowed:          *downloadAllowed,
		DownloadTranscodeAllowed: *downloadTx,
		TranscodeAllowed:         *transcodeTx,
		AudioTranscodeAllowed:    *audioTx,
		MaxStreams:               *maxStreams,
		MaxProfiles:              *maxProfiles,
		MaxTranscodes:            *maxTranscodes,
		AllowedPermissions:       permissions,
		RequestsAllowed:          *requestsAllowed,
	}, nil
}

// ReadAccountGroupInTransaction reads only a group assigned to this account's membership.
func ReadAccountGroupInTransaction(ctx context.Context, tx pgx.Tx, accountID int, id int64) (*Group, error) {
	return getGroupForAccount(ctx, tx, accountID, id)
}
