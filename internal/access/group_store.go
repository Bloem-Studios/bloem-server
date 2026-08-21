package access

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Group is an access group with its admin-facing member count.
type Group struct {
	ID                       int64
	OrganizationID           uuid.UUID
	Name                     string
	Description              string
	LibraryIDs               []int
	MaxPlaybackQuality       string
	PlaybackAllowed          bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int
	MaxProfiles              int
	MaxTranscodes            int
	AllowedPermissions       []string
	RequestsAllowed          bool
	IsDefault                bool
	ManagedTemplateKey       *string
	ManagedTemplateRevision  *int64
	MemberCount              int
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// Policy returns the group's policy layer as consumed by ApplyGroupPolicy.
func (g Group) Policy() GroupPolicy {
	return GroupPolicy{
		ID:                       g.ID,
		LibraryIDs:               cloneInts(g.LibraryIDs),
		MaxPlaybackQuality:       g.MaxPlaybackQuality,
		PlaybackAllowed:          g.PlaybackAllowed,
		DownloadAllowed:          g.DownloadAllowed,
		DownloadTranscodeAllowed: g.DownloadTranscodeAllowed,
		TranscodeAllowed:         g.TranscodeAllowed,
		AudioTranscodeAllowed:    g.AudioTranscodeAllowed,
		MaxStreams:               g.MaxStreams,
		MaxProfiles:              g.MaxProfiles,
		MaxTranscodes:            g.MaxTranscodes,
		AllowedPermissions:       cloneStrings(g.AllowedPermissions),
		RequestsAllowed:          g.RequestsAllowed,
	}
}

// CreateGroupInput contains the required fields for creating an access group.
type CreateGroupInput struct {
	Name                     string
	Description              string
	LibraryIDs               []int
	MaxPlaybackQuality       string
	PlaybackAllowed          *bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int
	MaxProfiles              int
	MaxTranscodes            int
	AllowedPermissions       []string
	RequestsAllowed          bool
	IsDefault                bool
}

// UpdateGroupInput contains optional fields for updating an access group.
type UpdateGroupInput struct {
	Name                     *string
	Description              *string
	LibraryIDs               *[]int
	MaxPlaybackQuality       *string
	PlaybackAllowed          *bool
	DownloadAllowed          *bool
	DownloadTranscodeAllowed *bool
	TranscodeAllowed         *bool
	AudioTranscodeAllowed    *bool
	MaxStreams               *int
	MaxProfiles              *int
	MaxTranscodes            *int
	AllowedPermissions       *[]string
	RequestsAllowed          *bool
	IsDefault                *bool
}

// GroupDeletionImpact reports the canonical reassignment performed while a
// non-default group is deleted.
type GroupDeletionImpact struct {
	ProfilesReassigned int   `json:"profiles_reassigned"`
	DefaultGroupID     int64 `json:"default_group_id"`
}

var (
	// ErrGroupNotFound reports that an access group does not exist.
	ErrGroupNotFound = errors.New("access group not found")
	// ErrGroupDuplicate reports a unique-name conflict.
	ErrGroupDuplicate = errors.New("access group already exists")
	// ErrDefaultGroupRequired reports an operation that would leave the server
	// without a default access group. New non-admin users are assigned to the
	// default group at creation, so losing it would create them ungrouped and
	// uncapped (the legacy per-user column defaults were retired).
	ErrDefaultGroupRequired = errors.New("a default access group is required")
	// ErrManagedGroup reports a generic mutation against a group owned by the
	// entitlement materializer. Managed groups change only through template
	// application so their source metadata and effective policy cannot diverge.
	ErrManagedGroup = errors.New("managed access groups can only be changed by applying an entitlement template")
)

// GroupStore persists access groups in Postgres.
type GroupStore struct {
	pool *pgxpool.Pool
}

// NewGroupStore creates a Postgres-backed access-group store.
func NewGroupStore(pool *pgxpool.Pool) *GroupStore {
	return &GroupStore{pool: pool}
}

const accessGroupSelectColumns = `g.id, g.organization_id, g.name, g.description, g.library_ids, g.max_playback_quality,
	g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
	g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
	g.max_transcodes, g.allowed_permissions, g.requests_allowed, g.is_default,
	g.managed_template_key, g.managed_template_revision, g.created_at, g.updated_at`

type groupScanner interface {
	Scan(dest ...any) error
}

func scanGroup(row groupScanner) (*Group, error) {
	var g Group
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
		&g.AllowedPermissions,
		&g.RequestsAllowed,
		&g.IsDefault,
		&g.ManagedTemplateKey,
		&g.ManagedTemplateRevision,
		&g.CreatedAt,
		&g.UpdatedAt,
		&g.MemberCount,
	); err != nil {
		return nil, err
	}
	return &g, nil
}

// List returns all access groups in an organization with profile member counts.
func (s *GroupStore) List(ctx context.Context, organizationID uuid.UUID) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+accessGroupSelectColumns+`, COUNT(p.id)::int AS member_count
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

	groups := []Group{}
	for rows.Next() {
		group, err := scanGroup(rows)
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
func (s *GroupStore) Get(ctx context.Context, organizationID uuid.UUID, id int64) (*Group, error) {
	group, err := scanGroup(s.pool.QueryRow(ctx, `
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
	group, err := scanGroup(s.pool.QueryRow(ctx, `
		SELECT `+accessGroupSelectColumns+`, COUNT(p.id)::int AS member_count
		FROM access_groups g
		LEFT JOIN user_profiles p
		  ON p.organization_id = g.organization_id
		 AND p.access_group_id = g.id
		WHERE g.id = $2
		  AND EXISTS (
			SELECT 1 FROM users u
			WHERE u.id = $1 AND u.access_group_id = g.id
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
	group, err := scanGroup(s.pool.QueryRow(ctx, `
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

// Create inserts a new access group.
func (s *GroupStore) Create(ctx context.Context, organizationID uuid.UUID, input CreateGroupInput) (*Group, error) {
	if organizationID == uuid.Nil {
		return nil, ErrGroupNotFound
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, fmt.Errorf("access group name is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning access group create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if input.IsDefault {
		if err := protectManagedDefault(ctx, tx, organizationID, 0); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET is_default = false
			WHERE organization_id = $1
			  AND is_default`, organizationID); err != nil {
			return nil, fmt.Errorf("clearing previous default access group: %w", err)
		}
	}

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO access_groups (
			organization_id, name, description, library_ids, max_playback_quality,
			playback_allowed, download_allowed, download_transcode_allowed,
			transcode_allowed, audio_transcode_allowed, max_streams, max_profiles,
			max_transcodes, allowed_permissions, requests_allowed, is_default
		)
		VALUES ($1, $2, $3, $4, $5, COALESCE($6, true), $7, $8, $9, $10,
		        $11, $12, $13, $14, $15, $16)
		RETURNING id`,
		organizationID,
		name,
		input.Description,
		input.LibraryIDs,
		NormalizePlaybackQuality(input.MaxPlaybackQuality),
		input.PlaybackAllowed,
		input.DownloadAllowed,
		input.DownloadTranscodeAllowed,
		input.TranscodeAllowed,
		input.AudioTranscodeAllowed,
		input.MaxStreams,
		input.MaxProfiles,
		input.MaxTranscodes,
		input.AllowedPermissions,
		input.RequestsAllowed,
		input.IsDefault,
	).Scan(&id)
	if err != nil {
		if isGroupDuplicate(err) {
			return nil, ErrGroupDuplicate
		}
		return nil, fmt.Errorf("creating access group: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing access group create: %w", err)
	}
	return s.Get(ctx, organizationID, id)
}

// Update modifies an access group. Setting IsDefault true clears the previous
// default, and every authorization-affecting change bumps member access-policy
// revisions in the same transaction. The current default cannot be demoted
// directly (which would leave no default) — promote another group instead.
func (s *GroupStore) Update(ctx context.Context, organizationID uuid.UUID, id int64, input UpdateGroupInput) (*Group, error) {
	sets := []string{}
	args := []any{}
	arg := 1

	if input.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", arg))
		args = append(args, strings.TrimSpace(*input.Name))
		arg++
	}
	if input.Description != nil {
		sets = append(sets, fmt.Sprintf("description = $%d", arg))
		args = append(args, *input.Description)
		arg++
	}
	if input.LibraryIDs != nil {
		sets = append(sets, fmt.Sprintf("library_ids = $%d", arg))
		args = append(args, *input.LibraryIDs)
		arg++
	}
	if input.MaxPlaybackQuality != nil {
		normalized := NormalizePlaybackQuality(*input.MaxPlaybackQuality)
		sets = append(sets, fmt.Sprintf("max_playback_quality = $%d", arg))
		args = append(args, normalized)
		arg++
	}
	if input.PlaybackAllowed != nil {
		sets = append(sets, fmt.Sprintf("playback_allowed = $%d", arg))
		args = append(args, *input.PlaybackAllowed)
		arg++
	}
	if input.DownloadAllowed != nil {
		sets = append(sets, fmt.Sprintf("download_allowed = $%d", arg))
		args = append(args, *input.DownloadAllowed)
		arg++
	}
	if input.DownloadTranscodeAllowed != nil {
		sets = append(sets, fmt.Sprintf("download_transcode_allowed = $%d", arg))
		args = append(args, *input.DownloadTranscodeAllowed)
		arg++
	}
	if input.TranscodeAllowed != nil {
		sets = append(sets, fmt.Sprintf("transcode_allowed = $%d", arg))
		args = append(args, *input.TranscodeAllowed)
		arg++
	}
	if input.AudioTranscodeAllowed != nil {
		sets = append(sets, fmt.Sprintf("audio_transcode_allowed = $%d", arg))
		args = append(args, *input.AudioTranscodeAllowed)
		arg++
	}
	if input.MaxStreams != nil {
		sets = append(sets, fmt.Sprintf("max_streams = $%d", arg))
		args = append(args, *input.MaxStreams)
		arg++
	}
	if input.MaxProfiles != nil {
		sets = append(sets, fmt.Sprintf("max_profiles = $%d", arg))
		args = append(args, *input.MaxProfiles)
		arg++
	}
	if input.MaxTranscodes != nil {
		sets = append(sets, fmt.Sprintf("max_transcodes = $%d", arg))
		args = append(args, *input.MaxTranscodes)
		arg++
	}
	if input.AllowedPermissions != nil {
		sets = append(sets, fmt.Sprintf("allowed_permissions = $%d", arg))
		args = append(args, *input.AllowedPermissions)
		arg++
	}
	if input.RequestsAllowed != nil {
		sets = append(sets, fmt.Sprintf("requests_allowed = $%d", arg))
		args = append(args, *input.RequestsAllowed)
		arg++
	}
	if input.IsDefault != nil {
		sets = append(sets, fmt.Sprintf("is_default = $%d", arg))
		args = append(args, *input.IsDefault)
		arg++
	}
	if len(sets) == 0 {
		return s.Get(ctx, organizationID, id)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning access group update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current Group
	if err := tx.QueryRow(ctx, `
			SELECT library_ids, max_playback_quality, playback_allowed,
				download_allowed, download_transcode_allowed, transcode_allowed,
				audio_transcode_allowed, max_streams, max_profiles, max_transcodes, allowed_permissions,
				requests_allowed, is_default, managed_template_key
			FROM access_groups
			WHERE organization_id = $1
			  AND id = $2
			FOR UPDATE`, organizationID, id).Scan(
		&current.LibraryIDs,
		&current.MaxPlaybackQuality,
		&current.PlaybackAllowed,
		&current.DownloadAllowed,
		&current.DownloadTranscodeAllowed,
		&current.TranscodeAllowed,
		&current.AudioTranscodeAllowed,
		&current.MaxStreams,
		&current.MaxProfiles,
		&current.MaxTranscodes,
		&current.AllowedPermissions,
		&current.RequestsAllowed,
		&current.IsDefault,
		&current.ManagedTemplateKey,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGroupNotFound
		}
		return nil, fmt.Errorf("loading access group for update: %w", err)
	}
	if current.ManagedTemplateKey != nil {
		return nil, ErrManagedGroup
	}
	authorizationChanged := groupAuthorizationChanged(current, input)
	if input.IsDefault != nil && *input.IsDefault {
		if err := protectManagedDefault(ctx, tx, organizationID, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET is_default = false
			WHERE organization_id = $1
			  AND is_default
			  AND id <> $2`, organizationID, id); err != nil {
			return nil, fmt.Errorf("clearing previous default access group: %w", err)
		}
	}
	if input.IsDefault != nil && !*input.IsDefault {
		if current.IsDefault {
			return nil, ErrDefaultGroupRequired
		}
	}

	sets = append(sets, "updated_at = NOW()")
	query := fmt.Sprintf("UPDATE access_groups SET %s WHERE organization_id = $%d AND id = $%d", strings.Join(sets, ", "), arg, arg+1)
	args = append(args, organizationID, id)
	tag, err := tx.Exec(ctx, query, args...)
	if err != nil {
		if isGroupDuplicate(err) {
			return nil, ErrGroupDuplicate
		}
		return nil, fmt.Errorf("updating access group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrGroupNotFound
	}
	if authorizationChanged {
		if _, err := tx.Exec(ctx, `
			UPDATE users
			SET access_policy_revision = access_policy_revision + 1
			WHERE id IN (
				SELECT DISTINCT user_id
				FROM user_profiles
				WHERE organization_id = $1
				  AND access_group_id = $2
			)`, organizationID, id); err != nil {
			return nil, fmt.Errorf("bumping access group member revisions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing access group update: %w", err)
	}
	return s.Get(ctx, organizationID, id)
}

func protectManagedDefault(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, replacementID int64) error {
	var currentID int64
	var managedKey *string
	err := tx.QueryRow(ctx, `
		SELECT id,managed_template_key FROM access_groups
		WHERE organization_id=$1 AND is_default
		FOR UPDATE`, organizationID).Scan(&currentID, &managedKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("locking current default access group: %w", err)
	}
	if managedKey != nil && currentID != replacementID {
		return ErrManagedGroup
	}
	return nil
}

// Delete removes an access group. Canonical profile assignments are moved to
// the organization's default group; the legacy account assignment is cleared
// by its FK. The default group cannot be deleted — promote another group first.
func (s *GroupStore) Delete(ctx context.Context, organizationID uuid.UUID, id int64) error {
	_, err := s.DeleteWithImpact(ctx, organizationID, id)
	return err
}

// DeleteWithImpact removes an access group and returns the exact number of
// profiles moved to the organization's default group.
func (s *GroupStore) DeleteWithImpact(ctx context.Context, organizationID uuid.UUID, id int64) (GroupDeletionImpact, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return GroupDeletionImpact{}, fmt.Errorf("beginning access group delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		isDefault          bool
		managedTemplateKey *string
	)
	err = tx.QueryRow(ctx, `
		SELECT is_default, managed_template_key
		FROM access_groups
		WHERE organization_id = $1
		  AND id = $2
		FOR UPDATE`, organizationID, id).Scan(&isDefault, &managedTemplateKey)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return GroupDeletionImpact{}, ErrGroupNotFound
	case err != nil:
		return GroupDeletionImpact{}, fmt.Errorf("checking access group default flag: %w", err)
	case managedTemplateKey != nil:
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

	if _, err := tx.Exec(ctx, `
		UPDATE users
		SET access_policy_revision = access_policy_revision + 1
		WHERE id IN (
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
	if subject.OrganizationID == uuid.Nil || subject.AccountID <= 0 {
		return nil, ErrGroupNotFound
	}
	if subject.ProfileID != "" {
		return nullableGroupPolicy(s.pool.QueryRow(ctx, `
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
	return nullableGroupPolicy(s.pool.QueryRow(ctx, `
		SELECT u.access_group_id, g.id, g.library_ids, g.max_playback_quality,
			g.playback_allowed, g.download_allowed, g.download_transcode_allowed,
			g.transcode_allowed, g.audio_transcode_allowed, g.max_streams, g.max_profiles,
			g.max_transcodes, g.allowed_permissions, g.requests_allowed
		FROM users u
		JOIN organizations o
		  ON o.id = $1
		 AND o.is_default
		LEFT JOIN access_groups g
		  ON g.organization_id = o.id
		 AND g.id = u.access_group_id
		WHERE u.id = $2`, subject.OrganizationID, subject.AccountID))
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

func isGroupDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
