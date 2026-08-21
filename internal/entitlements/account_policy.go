package entitlements

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	MaxAccountPolicySnapshotIDs = 10_000

	AccountPolicyStateManaged         = "managed"
	AccountPolicyStateCustom          = "custom"
	AccountPolicyStateLegacyUnmanaged = "legacy_unmanaged"

	AccountPolicyResultNotFound = "not_found"
	AccountPolicyResultStale    = "stale"
)

var (
	ErrAccountPolicySnapshotLimit = errors.New("entitlements: account policy snapshot limit exceeded")
	ErrInvalidAccountPolicyIDs    = errors.New("entitlements: invalid account policy ids")
)

// EffectivePolicySnapshot is the complete, currently resolved read projection.
// It is intentionally separate from Policy, whose JSON representation is part
// of durable cohort hashing and persistence.
type EffectivePolicySnapshot struct {
	LibraryIDs               []int    `json:"library_ids"`
	PlaybackAllowed          bool     `json:"playback_allowed"`
	MaxStreams               int      `json:"max_streams"`
	MaxProfiles              int      `json:"max_profiles"`
	TranscodeAllowed         bool     `json:"transcode_allowed"`
	AudioTranscodeAllowed    bool     `json:"audio_transcode_allowed"`
	MaxTranscodes            int      `json:"max_transcodes"`
	DownloadAllowed          bool     `json:"download_allowed"`
	DownloadTranscodeAllowed bool     `json:"download_transcode_allowed"`
	MaxPlaybackQuality       string   `json:"max_playback_quality"`
	AllowedPermissions       []string `json:"allowed_permissions"`
	RequestsAllowed          bool     `json:"requests_allowed"`
}

// ProfilePolicySnapshot is the current effective access-group policy for one
// profile. InheritsAccount is true only when the profile and account currently
// point at the same authoritative access group.
type ProfilePolicySnapshot struct {
	ProfileID       string                  `json:"profile_id"`
	ProfileName     string                  `json:"profile_name"`
	GroupID         int64                   `json:"group_id"`
	InheritsAccount bool                    `json:"inherits_account"`
	State           string                  `json:"state"`
	Policy          EffectivePolicySnapshot `json:"policy"`
}

// AccountPolicySnapshot is an authoritative point-in-time projection of the
// account and all of its profiles inside one organization.
type AccountPolicySnapshot struct {
	ObservedAt             time.Time               `json:"observed_at"`
	OrganizationID         uuid.UUID               `json:"organization_id"`
	AccountID              int                     `json:"account_id"`
	GroupID                int64                   `json:"group_id"`
	CohortID               uuid.UUID               `json:"cohort_id,omitempty"`
	CohortRevision         int64                   `json:"cohort_revision,omitempty"`
	SourceTemplateKey      string                  `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                   `json:"source_template_revision,omitempty"`
	State                  string                  `json:"state"`
	PolicyRevision         int64                   `json:"policy_revision"`
	Policy                 EffectivePolicySnapshot `json:"policy"`
	Profiles               []ProfilePolicySnapshot `json:"profiles"`
}

// AccountPolicySnapshotResult keeps bulk failures non-disclosing. Error is a
// stable safe code; database and ownership details never enter the response.
type AccountPolicySnapshotResult struct {
	AccountID int                    `json:"account_id"`
	Snapshot  *AccountPolicySnapshot `json:"snapshot,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// GetAccountPolicy returns one current policy projection. A nil organization
// selects the deployment's default organization for direct-account clients.
func (s *Store) GetAccountPolicy(ctx context.Context, organizationID uuid.UUID, accountID int) (AccountPolicySnapshot, error) {
	if s == nil || s.pool == nil || accountID <= 0 {
		return AccountPolicySnapshot{}, ErrAccountNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return AccountPolicySnapshot{}, fmt.Errorf("entitlements: begin account policy snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	observedAt, err := accountPolicyObservedAt(ctx, tx)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	snapshot, err := getAccountPolicyInTx(ctx, tx, organizationID, accountID, observedAt)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AccountPolicySnapshot{}, fmt.Errorf("entitlements: commit account policy snapshot: %w", err)
	}
	return snapshot, nil
}

// GetAccountPolicies returns bounded per-account results observed through one
// repeatable-read transaction and one Server timestamp.
func (s *Store) GetAccountPolicies(ctx context.Context, organizationID uuid.UUID, accountIDs []int) ([]AccountPolicySnapshotResult, time.Time, error) {
	if len(accountIDs) > MaxAccountPolicySnapshotIDs {
		return nil, time.Time{}, ErrAccountPolicySnapshotLimit
	}
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			return nil, time.Time{}, ErrInvalidAccountPolicyIDs
		}
	}
	if s == nil || s.pool == nil {
		return nil, time.Time{}, errors.New("entitlements: account policy store unavailable")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("entitlements: begin bulk account policy snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	observedAt, err := accountPolicyObservedAt(ctx, tx)
	if err != nil {
		return nil, time.Time{}, err
	}
	libraryIDs, err := currentEnabledLibraryIDs(ctx, tx)
	if err != nil {
		return nil, time.Time{}, err
	}
	accounts, err := loadAccountPolicyBatchAccounts(ctx, tx, organizationID, accountIDs)
	if err != nil {
		return nil, time.Time{}, err
	}
	profiles, err := loadAccountPolicyBatchProfiles(ctx, tx, organizationID, accountIDs)
	if err != nil {
		return nil, time.Time{}, err
	}

	items := make([]AccountPolicySnapshotResult, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		account, ok := accounts[accountID]
		if !ok {
			items = append(items, AccountPolicySnapshotResult{AccountID: accountID, Error: AccountPolicyResultNotFound})
			continue
		}
		snapshot := accountPolicySnapshotFromBatch(account, profiles[accountID], observedAt, libraryIDs)
		items = append(items, AccountPolicySnapshotResult{AccountID: accountID, Snapshot: &snapshot})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, time.Time{}, fmt.Errorf("entitlements: commit bulk account policy snapshot: %w", err)
	}
	return items, observedAt, nil
}

type accountPolicyBatchAccount struct {
	organizationID uuid.UUID
	user           models.User
	group          accountPolicyGroup
	groupPolicy    *accesspolicy.GroupPolicy
}

type accountPolicyBatchProfile struct {
	accountPolicyProfile
	groupPolicy *accesspolicy.GroupPolicy
}

func loadAccountPolicyBatchAccounts(ctx context.Context, tx pgx.Tx, requestedOrganizationID uuid.UUID, accountIDs []int) (map[int]accountPolicyBatchAccount, error) {
	rows, err := tx.Query(ctx, `
		SELECT memberships.organization_id,
		       users.id,users.role,users.permissions,users.library_ids,users.max_playback_quality,
		       users.access_policy_revision,users.max_streams,users.max_transcodes,
		       users.transcode_allowed,users.audio_transcode_allowed,users.max_profiles,
		       users.download_allowed,users.download_transcode_allowed,users.requests_allowed,
		       users.access_group_id,groups.id,groups.managed_template_key,
		       revisions.cohort_id,revisions.revision,
		       revisions.source_template_key,revisions.source_template_revision,
		       groups.library_ids,groups.max_playback_quality,groups.playback_allowed,
		       groups.download_allowed,groups.download_transcode_allowed,
		       groups.transcode_allowed,groups.audio_transcode_allowed,
		       groups.max_streams,groups.max_profiles,groups.max_transcodes,
		       groups.allowed_permissions,groups.requests_allowed
		FROM users
		JOIN organization_memberships memberships ON memberships.account_id=users.id
		JOIN organizations ON organizations.id=memberships.organization_id
		LEFT JOIN access_groups groups
		  ON groups.organization_id=memberships.organization_id
		 AND groups.id=users.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions revisions
		  ON revisions.organization_id=groups.organization_id
		 AND revisions.access_group_id=groups.id
		 AND revisions.id=groups.managed_cohort_id
		WHERE users.id=ANY($1::integer[])
		  AND (($2::boolean AND organizations.is_default) OR
		       (NOT $2::boolean AND memberships.organization_id=$3))`,
		accountIDs, requestedOrganizationID == uuid.Nil, requestedOrganizationID)
	if err != nil {
		return nil, fmt.Errorf("entitlements: load bulk account policy subjects: %w", err)
	}
	defer rows.Close()

	accounts := make(map[int]accountPolicyBatchAccount, len(accountIDs))
	for rows.Next() {
		var account accountPolicyBatchAccount
		var group accountPolicyGroupScan
		destinations := []any{
			&account.organizationID,
			&account.user.ID, &account.user.Role, &account.user.Permissions,
			&account.user.LibraryIDs, &account.user.MaxPlaybackQuality,
			&account.user.AccessPolicyRevision, &account.user.MaxStreams,
			&account.user.MaxTranscodes, &account.user.TranscodeAllowed,
			&account.user.AudioTranscodeAllowed, &account.user.MaxProfiles,
			&account.user.DownloadAllowed, &account.user.DownloadTranscodeAllowed,
			&account.user.RequestsAllowed,
		}
		destinations = append(destinations, group.destinations()...)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("entitlements: scan bulk account policy subject: %w", err)
		}
		account.group, account.groupPolicy, err = group.values()
		if errors.Is(err, ErrAccountNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		accounts[account.user.ID] = account
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate bulk account policy subjects: %w", err)
	}
	return accounts, nil
}

func loadAccountPolicyBatchProfiles(ctx context.Context, tx pgx.Tx, requestedOrganizationID uuid.UUID, accountIDs []int) (map[int][]accountPolicyBatchProfile, error) {
	rows, err := tx.Query(ctx, `
		SELECT profiles.user_id,profiles.id,profiles.name,
		       profiles.access_group_id,groups.id,groups.managed_template_key,
		       revisions.cohort_id,revisions.revision,
		       revisions.source_template_key,revisions.source_template_revision,
		       groups.library_ids,groups.max_playback_quality,groups.playback_allowed,
		       groups.download_allowed,groups.download_transcode_allowed,
		       groups.transcode_allowed,groups.audio_transcode_allowed,
		       groups.max_streams,groups.max_profiles,groups.max_transcodes,
		       groups.allowed_permissions,groups.requests_allowed
		FROM user_profiles profiles
		JOIN organization_memberships memberships
		  ON memberships.organization_id=profiles.organization_id
		 AND memberships.account_id=profiles.user_id
		JOIN organizations ON organizations.id=memberships.organization_id
		JOIN access_groups groups
		  ON groups.organization_id=profiles.organization_id
		 AND groups.id=profiles.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions revisions
		  ON revisions.organization_id=groups.organization_id
		 AND revisions.access_group_id=groups.id
		 AND revisions.id=groups.managed_cohort_id
		WHERE profiles.user_id=ANY($1::integer[])
		  AND (($2::boolean AND organizations.is_default) OR
		       (NOT $2::boolean AND memberships.organization_id=$3))
		ORDER BY profiles.user_id,profiles.created_at,profiles.id`,
		accountIDs, requestedOrganizationID == uuid.Nil, requestedOrganizationID)
	if err != nil {
		return nil, fmt.Errorf("entitlements: load bulk account policy profiles: %w", err)
	}
	defer rows.Close()

	profiles := make(map[int][]accountPolicyBatchProfile)
	for rows.Next() {
		var accountID int
		var profile accountPolicyBatchProfile
		var group accountPolicyGroupScan
		destinations := []any{&accountID, &profile.id, &profile.name}
		destinations = append(destinations, group.destinations()...)
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("entitlements: scan bulk account policy profile: %w", err)
		}
		profile.group, profile.groupPolicy, err = group.values()
		if err != nil {
			return nil, fmt.Errorf("entitlements: resolve bulk account policy profile group: %w", err)
		}
		profiles[accountID] = append(profiles[accountID], profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate bulk account policy profiles: %w", err)
	}
	return profiles, nil
}

func accountPolicySnapshotFromBatch(account accountPolicyBatchAccount, profiles []accountPolicyBatchProfile, observedAt time.Time, libraryIDs []int) AccountPolicySnapshot {
	effective := accountPolicyEffective(&account.user, account.groupPolicy)
	snapshot := AccountPolicySnapshot{
		ObservedAt: observedAt, OrganizationID: account.organizationID, AccountID: account.user.ID,
		GroupID: account.group.id, CohortID: account.group.cohortID,
		CohortRevision:         account.group.cohortRevision,
		SourceTemplateKey:      account.group.sourceTemplateKey,
		SourceTemplateRevision: account.group.sourceTemplateRevision,
		State:                  account.group.state(), PolicyRevision: account.user.AccessPolicyRevision,
		Policy: effectivePolicySnapshot(effective, libraryIDs), Profiles: []ProfilePolicySnapshot{},
	}
	for _, profile := range profiles {
		effective = accountPolicyEffective(&account.user, profile.groupPolicy)
		snapshot.Profiles = append(snapshot.Profiles, ProfilePolicySnapshot{
			ProfileID: profile.id, ProfileName: profile.name, GroupID: profile.group.id,
			InheritsAccount: profile.group.id != 0 && profile.group.id == account.group.id,
			State:           profile.group.state(), Policy: effectivePolicySnapshot(effective, libraryIDs),
		})
	}
	return snapshot
}

func accountPolicyEffective(user *models.User, group *accesspolicy.GroupPolicy) accesspolicy.EffectiveUserPolicy {
	if user != nil && user.Role == models.RoleAdmin {
		group = nil
	}
	return accesspolicy.ApplyGroupPolicy(user, group)
}

func accountPolicyObservedAt(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var observedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&observedAt); err != nil {
		return time.Time{}, fmt.Errorf("entitlements: observe account policies: %w", err)
	}
	return observedAt.UTC(), nil
}

func getAccountPolicyInTx(ctx context.Context, tx pgx.Tx, requestedOrganizationID uuid.UUID, accountID int, observedAt time.Time) (AccountPolicySnapshot, error) {
	organizationID, err := resolveAccountPolicyOrganization(ctx, tx, requestedOrganizationID, accountID)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	user, group, err := loadAccountPolicyUser(ctx, tx, organizationID, accountID)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	libraryIDs, err := currentEnabledLibraryIDs(ctx, tx)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	provider := accountPolicyGroupProvider{tx: tx}
	effective, err := accesspolicy.EffectivePolicyForSubject(ctx, user, accesspolicy.GroupSubject{
		OrganizationID: organizationID,
		AccountID:      accountID,
	}, provider)
	if err != nil {
		return AccountPolicySnapshot{}, fmt.Errorf("entitlements: resolve account policy: %w", err)
	}

	snapshot := AccountPolicySnapshot{
		ObservedAt: observedAt, OrganizationID: organizationID, AccountID: accountID,
		GroupID: group.id, CohortID: group.cohortID, CohortRevision: group.cohortRevision,
		SourceTemplateKey:      group.sourceTemplateKey,
		SourceTemplateRevision: group.sourceTemplateRevision,
		State:                  group.state(), PolicyRevision: user.AccessPolicyRevision,
		Policy: effectivePolicySnapshot(effective, libraryIDs), Profiles: []ProfilePolicySnapshot{},
	}

	profiles, err := loadAccountPolicyProfiles(ctx, tx, organizationID, accountID)
	if err != nil {
		return AccountPolicySnapshot{}, err
	}
	for _, profile := range profiles {
		effective, err := accesspolicy.EffectivePolicyForSubject(ctx, user, accesspolicy.GroupSubject{
			OrganizationID: organizationID,
			AccountID:      accountID,
			ProfileID:      profile.id,
		}, provider)
		if err != nil {
			return AccountPolicySnapshot{}, fmt.Errorf("entitlements: resolve profile policy: %w", err)
		}
		snapshot.Profiles = append(snapshot.Profiles, ProfilePolicySnapshot{
			ProfileID: profile.id, ProfileName: profile.name, GroupID: profile.group.id,
			InheritsAccount: profile.group.id != 0 && profile.group.id == group.id,
			State:           profile.group.state(), Policy: effectivePolicySnapshot(effective, libraryIDs),
		})
	}
	return snapshot, nil
}

func resolveAccountPolicyOrganization(ctx context.Context, tx pgx.Tx, requested uuid.UUID, accountID int) (uuid.UUID, error) {
	var organizationID uuid.UUID
	var err error
	if requested == uuid.Nil {
		err = tx.QueryRow(ctx, `
			SELECT organizations.id
			FROM organization_memberships
			JOIN organizations ON organizations.id=organization_memberships.organization_id
			WHERE organization_memberships.account_id=$1 AND organizations.is_default`, accountID).Scan(&organizationID)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT organization_id
			FROM organization_memberships
			WHERE organization_id=$1 AND account_id=$2`, requested, accountID).Scan(&organizationID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrAccountNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("entitlements: resolve account policy organization: %w", err)
	}
	return organizationID, nil
}

type accountPolicyGroup struct {
	id                     int64
	managedTemplateKey     string
	cohortID               uuid.UUID
	cohortRevision         int64
	sourceTemplateKey      string
	sourceTemplateRevision int64
}

func (g accountPolicyGroup) state() string {
	switch {
	case g.cohortID != uuid.Nil:
		return AccountPolicyStateManaged
	case g.id == 0 || g.managedTemplateKey != "":
		return AccountPolicyStateLegacyUnmanaged
	default:
		return AccountPolicyStateCustom
	}
}

func loadAccountPolicyUser(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) (*models.User, accountPolicyGroup, error) {
	var (
		user                   models.User
		groupID                *int64
		managedTemplateKey     *string
		cohortID               *uuid.UUID
		cohortRevision         *int64
		sourceTemplateKey      *string
		sourceTemplateRevision *int64
	)
	err := tx.QueryRow(ctx, `
		SELECT u.id,u.role,u.permissions,u.library_ids,u.max_playback_quality,
		       u.access_policy_revision,u.max_streams,u.max_transcodes,
		       u.transcode_allowed,u.audio_transcode_allowed,u.max_profiles,
		       u.download_allowed,u.download_transcode_allowed,u.requests_allowed,
		       u.access_group_id,g.id,g.managed_template_key,revisions.cohort_id,
		       revisions.revision,revisions.source_template_key,revisions.source_template_revision
		FROM users u
		LEFT JOIN access_groups g
		  ON g.organization_id=$1 AND g.id=u.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions revisions
		  ON revisions.organization_id=g.organization_id
		 AND revisions.access_group_id=g.id
		 AND revisions.id=g.managed_cohort_id
		WHERE u.id=$2`, organizationID, accountID).Scan(
		&user.ID, &user.Role, &user.Permissions, &user.LibraryIDs, &user.MaxPlaybackQuality,
		&user.AccessPolicyRevision, &user.MaxStreams, &user.MaxTranscodes,
		&user.TranscodeAllowed, &user.AudioTranscodeAllowed, &user.MaxProfiles,
		&user.DownloadAllowed, &user.DownloadTranscodeAllowed, &user.RequestsAllowed,
		&user.AccessGroupID, &groupID, &managedTemplateKey, &cohortID,
		&cohortRevision, &sourceTemplateKey, &sourceTemplateRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, accountPolicyGroup{}, ErrAccountNotFound
	}
	if err != nil {
		return nil, accountPolicyGroup{}, fmt.Errorf("entitlements: load account policy subject: %w", err)
	}
	return &user, makeAccountPolicyGroup(groupID, managedTemplateKey, cohortID, cohortRevision, sourceTemplateKey, sourceTemplateRevision), nil
}

type accountPolicyProfile struct {
	id    string
	name  string
	group accountPolicyGroup
}

func loadAccountPolicyProfiles(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, accountID int) ([]accountPolicyProfile, error) {
	rows, err := tx.Query(ctx, `
		SELECT profiles.id,profiles.name,groups.id,groups.managed_template_key,
		       revisions.cohort_id,revisions.revision,
		       revisions.source_template_key,revisions.source_template_revision
		FROM user_profiles profiles
		JOIN access_groups groups
		  ON groups.organization_id=profiles.organization_id
		 AND groups.id=profiles.access_group_id
		LEFT JOIN entitlement_policy_cohort_revisions revisions
		  ON revisions.organization_id=groups.organization_id
		 AND revisions.access_group_id=groups.id
		 AND revisions.id=groups.managed_cohort_id
		WHERE profiles.organization_id=$1 AND profiles.user_id=$2
		ORDER BY profiles.created_at,profiles.id`, organizationID, accountID)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list account policy profiles: %w", err)
	}
	defer rows.Close()
	profiles := []accountPolicyProfile{}
	for rows.Next() {
		var (
			profile                accountPolicyProfile
			groupID                *int64
			managedTemplateKey     *string
			cohortID               *uuid.UUID
			cohortRevision         *int64
			sourceTemplateKey      *string
			sourceTemplateRevision *int64
		)
		if err := rows.Scan(&profile.id, &profile.name, &groupID, &managedTemplateKey,
			&cohortID, &cohortRevision, &sourceTemplateKey, &sourceTemplateRevision); err != nil {
			return nil, fmt.Errorf("entitlements: scan account policy profile: %w", err)
		}
		profile.group = makeAccountPolicyGroup(groupID, managedTemplateKey, cohortID, cohortRevision, sourceTemplateKey, sourceTemplateRevision)
		profiles = append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate account policy profiles: %w", err)
	}
	return profiles, nil
}

func makeAccountPolicyGroup(groupID *int64, managedTemplateKey *string, cohortID *uuid.UUID, cohortRevision *int64, sourceTemplateKey *string, sourceTemplateRevision *int64) accountPolicyGroup {
	var group accountPolicyGroup
	if groupID != nil {
		group.id = *groupID
	}
	if managedTemplateKey != nil {
		group.managedTemplateKey = *managedTemplateKey
	}
	if cohortID != nil {
		group.cohortID = *cohortID
	}
	if cohortRevision != nil {
		group.cohortRevision = *cohortRevision
	}
	if sourceTemplateKey != nil {
		group.sourceTemplateKey = *sourceTemplateKey
	}
	if sourceTemplateRevision != nil {
		group.sourceTemplateRevision = *sourceTemplateRevision
	}
	return group
}

type accountPolicyGroupScan struct {
	assignedGroupID                        *int64
	groupID                                *int64
	managedTemplateKey                     *string
	cohortID                               *uuid.UUID
	cohortRevision                         *int64
	sourceTemplateKey                      *string
	sourceTemplateRevision                 *int64
	libraryIDs                             []int
	maxQuality                             *string
	playbackAllowed                        *bool
	downloadAllowed                        *bool
	downloadTranscodeAllowed               *bool
	transcodeAllowed                       *bool
	audioTranscodeAllowed                  *bool
	maxStreams, maxProfiles, maxTranscodes *int
	allowedPermissions                     []string
	requestsAllowed                        *bool
}

func (s *accountPolicyGroupScan) destinations() []any {
	return []any{
		&s.assignedGroupID, &s.groupID, &s.managedTemplateKey,
		&s.cohortID, &s.cohortRevision, &s.sourceTemplateKey,
		&s.sourceTemplateRevision, &s.libraryIDs, &s.maxQuality,
		&s.playbackAllowed, &s.downloadAllowed, &s.downloadTranscodeAllowed,
		&s.transcodeAllowed, &s.audioTranscodeAllowed, &s.maxStreams,
		&s.maxProfiles, &s.maxTranscodes, &s.allowedPermissions,
		&s.requestsAllowed,
	}
}

func (s *accountPolicyGroupScan) values() (accountPolicyGroup, *accesspolicy.GroupPolicy, error) {
	if s.assignedGroupID == nil {
		return accountPolicyGroup{}, nil, nil
	}
	if s.groupID == nil || s.maxQuality == nil || s.playbackAllowed == nil ||
		s.downloadAllowed == nil || s.downloadTranscodeAllowed == nil ||
		s.transcodeAllowed == nil || s.audioTranscodeAllowed == nil ||
		s.maxStreams == nil || s.maxProfiles == nil || s.maxTranscodes == nil ||
		s.requestsAllowed == nil {
		return accountPolicyGroup{}, nil, ErrAccountNotFound
	}
	group := makeAccountPolicyGroup(
		s.groupID, s.managedTemplateKey, s.cohortID, s.cohortRevision,
		s.sourceTemplateKey, s.sourceTemplateRevision,
	)
	policy := &accesspolicy.GroupPolicy{
		ID: *s.groupID, LibraryIDs: s.libraryIDs, MaxPlaybackQuality: *s.maxQuality,
		PlaybackAllowed: *s.playbackAllowed, DownloadAllowed: *s.downloadAllowed,
		DownloadTranscodeAllowed: *s.downloadTranscodeAllowed,
		TranscodeAllowed:         *s.transcodeAllowed,
		AudioTranscodeAllowed:    *s.audioTranscodeAllowed,
		MaxStreams:               *s.maxStreams,
		MaxProfiles:              *s.maxProfiles,
		MaxTranscodes:            *s.maxTranscodes,
		AllowedPermissions:       s.allowedPermissions,
		RequestsAllowed:          *s.requestsAllowed,
	}
	return group, policy, nil
}

type accountPolicyGroupProvider struct{ tx pgx.Tx }

func (p accountPolicyGroupProvider) ResolvePolicy(ctx context.Context, subject accesspolicy.GroupSubject) (*accesspolicy.GroupPolicy, error) {
	if subject.OrganizationID == uuid.Nil || subject.AccountID <= 0 {
		return nil, ErrAccountNotFound
	}
	if subject.ProfileID != "" {
		return scanAccountPolicyGroupPolicy(p.tx.QueryRow(ctx, `
			SELECT profiles.access_group_id,groups.id,groups.library_ids,
			       groups.max_playback_quality,groups.playback_allowed,
			       groups.download_allowed,groups.download_transcode_allowed,
			       groups.transcode_allowed,groups.audio_transcode_allowed,
			       groups.max_streams,groups.max_profiles,groups.max_transcodes,
			       groups.allowed_permissions,groups.requests_allowed
			FROM user_profiles profiles
			LEFT JOIN access_groups groups
			  ON groups.organization_id=profiles.organization_id
			 AND groups.id=profiles.access_group_id
			WHERE profiles.organization_id=$1 AND profiles.user_id=$2 AND profiles.id=$3`,
			subject.OrganizationID, subject.AccountID, subject.ProfileID))
	}
	return scanAccountPolicyGroupPolicy(p.tx.QueryRow(ctx, `
		SELECT users.access_group_id,groups.id,groups.library_ids,
		       groups.max_playback_quality,groups.playback_allowed,
		       groups.download_allowed,groups.download_transcode_allowed,
		       groups.transcode_allowed,groups.audio_transcode_allowed,
		       groups.max_streams,groups.max_profiles,groups.max_transcodes,
		       groups.allowed_permissions,groups.requests_allowed
		FROM users
		LEFT JOIN access_groups groups
		  ON groups.organization_id=$1 AND groups.id=users.access_group_id
		WHERE users.id=$2`, subject.OrganizationID, subject.AccountID))
}

func scanAccountPolicyGroupPolicy(row pgx.Row) (*accesspolicy.GroupPolicy, error) {
	var (
		assignedGroupID, groupID                                   *int64
		libraryIDs                                                 []int
		maxQuality                                                 *string
		playbackAllowed, downloadAllowed, downloadTranscodeAllowed *bool
		transcodeAllowed, audioTranscodeAllowed, requestsAllowed   *bool
		maxStreams, maxProfiles, maxTranscodes                     *int
		allowedPermissions                                         []string
	)
	err := row.Scan(&assignedGroupID, &groupID, &libraryIDs, &maxQuality,
		&playbackAllowed, &downloadAllowed, &downloadTranscodeAllowed,
		&transcodeAllowed, &audioTranscodeAllowed, &maxStreams, &maxProfiles,
		&maxTranscodes, &allowedPermissions, &requestsAllowed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("entitlements: load effective group policy: %w", err)
	}
	if assignedGroupID == nil {
		return nil, nil
	}
	if groupID == nil || maxQuality == nil || playbackAllowed == nil || downloadAllowed == nil ||
		downloadTranscodeAllowed == nil || transcodeAllowed == nil || audioTranscodeAllowed == nil ||
		maxStreams == nil || maxProfiles == nil || maxTranscodes == nil || requestsAllowed == nil {
		return nil, ErrAccountNotFound
	}
	return &accesspolicy.GroupPolicy{
		ID: *groupID, LibraryIDs: libraryIDs, MaxPlaybackQuality: *maxQuality,
		PlaybackAllowed: *playbackAllowed, DownloadAllowed: *downloadAllowed,
		DownloadTranscodeAllowed: *downloadTranscodeAllowed,
		TranscodeAllowed:         *transcodeAllowed, AudioTranscodeAllowed: *audioTranscodeAllowed,
		MaxStreams: *maxStreams, MaxProfiles: *maxProfiles, MaxTranscodes: *maxTranscodes,
		AllowedPermissions: allowedPermissions, RequestsAllowed: *requestsAllowed,
	}, nil
}

func currentEnabledLibraryIDs(ctx context.Context, tx pgx.Tx) ([]int, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM media_folders WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list current policy libraries: %w", err)
	}
	defer rows.Close()
	result := []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("entitlements: scan current policy library: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate current policy libraries: %w", err)
	}
	return result, nil
}

func effectivePolicySnapshot(effective accesspolicy.EffectiveUserPolicy, currentLibraryIDs []int) EffectivePolicySnapshot {
	libraryIDs := effective.LibraryIDs
	if libraryIDs == nil {
		libraryIDs = currentLibraryIDs
	}
	return EffectivePolicySnapshot{
		LibraryIDs: append([]int{}, libraryIDs...), PlaybackAllowed: effective.PlaybackAllowed,
		MaxStreams: effective.MaxStreams, MaxProfiles: effective.MaxProfiles,
		TranscodeAllowed: effective.TranscodeAllowed, AudioTranscodeAllowed: effective.AudioTranscodeAllowed,
		MaxTranscodes:            effective.MaxTranscodes,
		DownloadAllowed:          effective.DownloadAllowed,
		DownloadTranscodeAllowed: effective.DownloadTranscodeAllowed,
		MaxPlaybackQuality:       effective.MaxPlaybackQuality,
		AllowedPermissions:       appendNilPreservingStrings(effective.Permissions),
		RequestsAllowed:          effective.RequestsAllowed,
	}
}

func appendNilPreservingStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
