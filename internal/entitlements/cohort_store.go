package entitlements

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/permissioncatalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrCohortNotFound reports a cohort outside the requested organization or
	// an organization that does not exist.
	ErrCohortNotFound = errors.New("entitlements: cohort not found")
)

const (
	PolicySetAdd          = "add"
	PolicySetRemove       = "remove"
	PolicySetReplace      = "replace"
	PolicyLibrariesAll    = "all"
	PolicyLibrariesNone   = "none"
	PolicySetUnrestricted = "unrestricted"
)

// IntegerSetPatch applies an explicit operation to a policy's integer set.
// Values are ignored only for the all and none operations.
type IntegerSetPatch struct {
	Mode   string
	Values []int
}

// StringSetPatch applies an explicit operation to a policy's string set.
// Values are ignored only for the unrestricted operation.
type StringSetPatch struct {
	Mode   string
	Values []string
}

// PolicyPatch describes deterministic changes to a complete effective policy.
// Nil fields remain unchanged.
type PolicyPatch struct {
	LibraryIDs               *IntegerSetPatch
	PlaybackAllowed          *bool
	MaxStreams               *int
	MaxProfiles              *int
	TranscodeAllowed         *bool
	MaxTranscodes            *int
	DownloadAllowed          *bool
	DownloadTranscodeAllowed *bool
	MaxPlaybackQuality       *string
	AllowedPermissions       *StringSetPatch
	RequestsAllowed          *bool
}

// CohortRevision is one immutable organization policy and its protected access
// group. ID identifies this exact revision; ParentID identifies the exact
// parent revision for derived policies.
type CohortRevision struct {
	ID                     uuid.UUID
	OrganizationID         uuid.UUID
	Name                   string
	Revision               int64
	AccessGroupID          int64
	SourceTemplateKey      string
	SourceTemplateRevision int64
	ParentID               uuid.UUID
	DerivationKind         string
	Policy                 Policy
	PolicyDigest           string
	Archived               bool
	CreatedByAccountID     int
	CreatedAt              time.Time
}

// EnsureExactCohortInTx returns the one cohort for an exact template revision.
// Existing template-managed groups are adopted by adding provenance only; the
// group's policy, default state, and assignments are never rewritten.
func (s *Store) EnsureExactCohortInTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64, actorID int) (CohortRevision, bool, error) {
	key = strings.TrimSpace(key)
	if organizationID == uuid.Nil || key == "" || revision <= 0 {
		return CohortRevision{}, false, fmt.Errorf("%w: organization and template revision are required", ErrInvalidPolicy)
	}
	if err := lockCohortOrganization(ctx, tx, organizationID); err != nil {
		return CohortRevision{}, false, err
	}
	if existing, found, err := getExactCohort(ctx, tx, organizationID, key, revision); err != nil {
		return CohortRevision{}, false, err
	} else if found {
		return existing, false, nil
	}
	if actorID <= 0 {
		return CohortRevision{}, false, fmt.Errorf("%w: actor is required to create an exact cohort", ErrInvalidPolicy)
	}

	template, err := getTemplate(ctx, tx, key, revision, false)
	if err != nil {
		return CohortRevision{}, false, err
	}
	if !template.Enabled || template.Archived {
		return CohortRevision{}, false, ErrTemplateUnavailable
	}

	groupID, policy, found, err := loadUnadoptedTemplateGroup(ctx, tx, organizationID, key, revision)
	if err != nil {
		return CohortRevision{}, false, err
	}
	if !found {
		policy, err = resolveMaterializedPolicy(ctx, tx, template.Policy)
		if err != nil {
			return CohortRevision{}, false, err
		}
	}
	policy, err = normalizeResolvedPolicy(policy)
	if err != nil {
		return CohortRevision{}, false, err
	}
	policy, err = resolveMaterializedPolicy(ctx, tx, policy)
	if err != nil {
		return CohortRevision{}, false, err
	}
	digest, err := cohortPolicyDigest(policy)
	if err != nil {
		return CohortRevision{}, false, err
	}

	cohortID := uuid.New()
	revisionID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlement_policy_cohorts (id,organization_id,name)
		VALUES ($1,$2,$3)`, cohortID, organizationID, template.Name); err != nil {
		return CohortRevision{}, false, fmt.Errorf("entitlements: create exact cohort identity: %w", err)
	}
	if !found {
		groupID, err = insertCohortGroup(ctx, tx, organizationID, revisionID, template.Key, template.Revision, template.Name, policy)
		if err != nil {
			return CohortRevision{}, false, err
		}
	}
	result := CohortRevision{
		ID: revisionID, OrganizationID: organizationID, Name: template.Name, Revision: 1,
		AccessGroupID: groupID, SourceTemplateKey: template.Key,
		SourceTemplateRevision: template.Revision, DerivationKind: "exact_template",
		Policy: policy, PolicyDigest: digest, CreatedByAccountID: actorID,
	}
	if err := insertCohortRevision(ctx, tx, cohortID, result); err != nil {
		return CohortRevision{}, false, err
	}
	if found {
		if _, err := tx.Exec(ctx, `
			UPDATE access_groups
			SET managed_cohort_id=$3
			WHERE organization_id=$1 AND id=$2`, organizationID, groupID, revisionID); err != nil {
			return CohortRevision{}, false, fmt.Errorf("entitlements: adopt template-managed group: %w", err)
		}
	}
	loaded, err := getCohort(ctx, tx, organizationID, revisionID)
	if err != nil {
		return CohortRevision{}, false, err
	}
	return loaded, true, nil
}

// DeriveCohortInTx creates or reuses a child cohort with a complete patched
// policy. It always uses a separate protected access group from its parent.
func (s *Store) DeriveCohortInTx(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, parentID uuid.UUID, name string, patch PolicyPatch, actorID int) (CohortRevision, bool, error) {
	name = strings.TrimSpace(name)
	if organizationID == uuid.Nil || parentID == uuid.Nil || name == "" || actorID <= 0 {
		return CohortRevision{}, false, fmt.Errorf("%w: organization, parent, name, and actor are required", ErrInvalidPolicy)
	}
	if err := lockCohortOrganization(ctx, tx, organizationID); err != nil {
		return CohortRevision{}, false, err
	}
	parent, err := getCohort(ctx, tx, organizationID, parentID)
	if err != nil {
		return CohortRevision{}, false, err
	}
	policy, err := applyPolicyPatch(ctx, tx, parent.Policy, patch)
	if err != nil {
		return CohortRevision{}, false, err
	}
	digest, err := cohortPolicyDigest(policy)
	if err != nil {
		return CohortRevision{}, false, err
	}
	if existing, found, err := getDerivedCohort(ctx, tx, organizationID, parentID, name, digest); err != nil {
		return CohortRevision{}, false, err
	} else if found {
		return existing, false, nil
	}

	cohortID := uuid.New()
	revisionID := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO entitlement_policy_cohorts (id,organization_id,name)
		VALUES ($1,$2,$3)`, cohortID, organizationID, name); err != nil {
		return CohortRevision{}, false, fmt.Errorf("entitlements: create derived cohort identity: %w", err)
	}
	groupID, err := insertCohortGroup(
		ctx, tx, organizationID, revisionID, parent.SourceTemplateKey,
		parent.SourceTemplateRevision, name, policy,
	)
	if err != nil {
		return CohortRevision{}, false, err
	}
	result := CohortRevision{
		ID: revisionID, OrganizationID: organizationID, Name: name, Revision: 1,
		AccessGroupID: groupID, SourceTemplateKey: parent.SourceTemplateKey,
		SourceTemplateRevision: parent.SourceTemplateRevision, ParentID: parent.ID,
		DerivationKind: "policy_patch", Policy: policy, PolicyDigest: digest,
		CreatedByAccountID: actorID,
	}
	if err := insertCohortRevision(ctx, tx, cohortID, result); err != nil {
		return CohortRevision{}, false, err
	}
	loaded, err := getCohort(ctx, tx, organizationID, revisionID)
	if err != nil {
		return CohortRevision{}, false, err
	}
	return loaded, true, nil
}

// ListCohorts returns immutable cohort revisions for one organization.
func (s *Store) ListCohorts(ctx context.Context, organizationID uuid.UUID, includeArchived bool) ([]CohortRevision, error) {
	if organizationID == uuid.Nil {
		return nil, ErrCohortNotFound
	}
	query := cohortSelect + ` WHERE r.organization_id=$1`
	if !includeArchived {
		query += ` AND NOT c.archived`
	}
	query += ` ORDER BY lower(r.name), r.revision DESC, r.id`
	rows, err := s.pool.Query(ctx, query, organizationID)
	if err != nil {
		return nil, fmt.Errorf("entitlements: list cohorts: %w", err)
	}
	defer rows.Close()
	result := []CohortRevision{}
	for rows.Next() {
		item, err := scanCohort(rows)
		if err != nil {
			return nil, fmt.Errorf("entitlements: scan cohort list: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entitlements: iterate cohort list: %w", err)
	}
	return result, nil
}

// GetCohort returns one exact revision scoped to its organization.
func (s *Store) GetCohort(ctx context.Context, organizationID, cohortID uuid.UUID) (CohortRevision, error) {
	if organizationID == uuid.Nil || cohortID == uuid.Nil {
		return CohortRevision{}, ErrCohortNotFound
	}
	return getCohort(ctx, s.pool, organizationID, cohortID)
}

func lockCohortOrganization(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID) error {
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, organizationID).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return ErrCohortNotFound
	} else if err != nil {
		return fmt.Errorf("entitlements: lock cohort organization: %w", err)
	}
	return nil
}

func loadUnadoptedTemplateGroup(ctx context.Context, tx pgx.Tx, organizationID uuid.UUID, key string, revision int64) (int64, Policy, bool, error) {
	var id int64
	var policy Policy
	err := tx.QueryRow(ctx, `
		SELECT id,library_ids,playback_allowed,max_streams,max_profiles,
		       transcode_allowed,max_transcodes,download_allowed,
		       download_transcode_allowed,max_playback_quality,
		       allowed_permissions,requests_allowed
		FROM access_groups
		WHERE organization_id=$1
		  AND managed_template_key=$2
		  AND managed_template_revision=$3
		  AND managed_cohort_id IS NULL
		ORDER BY is_default DESC,id
		LIMIT 1
		FOR UPDATE`, organizationID, key, revision).Scan(
		&id, &policy.LibraryIDs, &policy.PlaybackAllowed, &policy.MaxStreams,
		&policy.MaxProfiles, &policy.TranscodeAllowed, &policy.MaxTranscodes,
		&policy.DownloadAllowed, &policy.DownloadTranscodeAllowed,
		&policy.MaxPlaybackQuality, &policy.AllowedPermissions, &policy.RequestsAllowed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, Policy{}, false, nil
	}
	if err != nil {
		return 0, Policy{}, false, fmt.Errorf("entitlements: load adoptable template group: %w", err)
	}
	return id, policy, true, nil
}

func insertCohortGroup(ctx context.Context, tx pgx.Tx, organizationID, revisionID uuid.UUID, templateKey string, templateRevision int64, name string, policy Policy) (int64, error) {
	groupName := fmt.Sprintf("Managed Cohort %s [%s]", name, revisionID.String()[:8])
	var groupID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO access_groups (
			organization_id,name,description,is_default,library_ids,
			max_playback_quality,playback_allowed,download_allowed,
			download_transcode_allowed,max_streams,max_profiles,
			transcode_allowed,max_transcodes,allowed_permissions,
			requests_allowed,managed_template_key,managed_template_revision,
			managed_cohort_id
		) VALUES (
			$1,$2,'Managed from an immutable entitlement policy cohort.',false,$3,
			$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16
		) RETURNING id`,
		organizationID, groupName, policy.LibraryIDs, policy.MaxPlaybackQuality,
		policy.PlaybackAllowed, policy.DownloadAllowed, policy.DownloadTranscodeAllowed,
		policy.MaxStreams, policy.MaxProfiles, policy.TranscodeAllowed,
		policy.MaxTranscodes, policy.AllowedPermissions, policy.RequestsAllowed,
		templateKey, templateRevision, revisionID,
	).Scan(&groupID)
	if err != nil {
		return 0, fmt.Errorf("entitlements: create cohort access group: %w", err)
	}
	return groupID, nil
}

func insertCohortRevision(ctx context.Context, tx pgx.Tx, cohortID uuid.UUID, revision CohortRevision) error {
	var parent any
	if revision.ParentID != uuid.Nil {
		parent = revision.ParentID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO entitlement_policy_cohort_revisions (
			id,cohort_id,organization_id,name,revision,access_group_id,
			source_template_key,source_template_revision,parent_id,derivation_kind,
			library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
			max_transcodes,download_allowed,download_transcode_allowed,
			max_playback_quality,allowed_permissions,requests_allowed,policy_digest,
			created_by_account_id
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			$19,$20,$21,$22,$23
		)`,
		revision.ID, cohortID, revision.OrganizationID, revision.Name, revision.Revision,
		revision.AccessGroupID, revision.SourceTemplateKey, revision.SourceTemplateRevision,
		parent, revision.DerivationKind, revision.Policy.LibraryIDs,
		revision.Policy.PlaybackAllowed, revision.Policy.MaxStreams,
		revision.Policy.MaxProfiles, revision.Policy.TranscodeAllowed,
		revision.Policy.MaxTranscodes, revision.Policy.DownloadAllowed,
		revision.Policy.DownloadTranscodeAllowed, revision.Policy.MaxPlaybackQuality,
		revision.Policy.AllowedPermissions, revision.Policy.RequestsAllowed,
		revision.PolicyDigest, revision.CreatedByAccountID,
	)
	if err != nil {
		return fmt.Errorf("entitlements: create cohort revision: %w", err)
	}
	return nil
}

func applyPolicyPatch(ctx context.Context, tx pgx.Tx, policy Policy, patch PolicyPatch) (Policy, error) {
	if policy.LibraryIDs != nil {
		policy.LibraryIDs = append([]int{}, policy.LibraryIDs...)
	}
	if policy.AllowedPermissions != nil {
		policy.AllowedPermissions = append([]string{}, policy.AllowedPermissions...)
	}
	var err error
	if patch.LibraryIDs != nil {
		policy.LibraryIDs, err = patchIntegerSet(policy.LibraryIDs, *patch.LibraryIDs)
		if err != nil {
			return Policy{}, err
		}
	}
	if patch.PlaybackAllowed != nil {
		policy.PlaybackAllowed = *patch.PlaybackAllowed
	}
	if patch.MaxStreams != nil {
		policy.MaxStreams = *patch.MaxStreams
	}
	if patch.MaxProfiles != nil {
		policy.MaxProfiles = *patch.MaxProfiles
	}
	if patch.TranscodeAllowed != nil {
		policy.TranscodeAllowed = *patch.TranscodeAllowed
	}
	if patch.MaxTranscodes != nil {
		policy.MaxTranscodes = *patch.MaxTranscodes
	}
	if patch.DownloadAllowed != nil {
		policy.DownloadAllowed = *patch.DownloadAllowed
	}
	if patch.DownloadTranscodeAllowed != nil {
		policy.DownloadTranscodeAllowed = *patch.DownloadTranscodeAllowed
	}
	if patch.MaxPlaybackQuality != nil {
		policy.MaxPlaybackQuality = *patch.MaxPlaybackQuality
	}
	if patch.AllowedPermissions != nil {
		policy.AllowedPermissions, err = patchStringSet(policy.AllowedPermissions, *patch.AllowedPermissions)
		if err != nil {
			return Policy{}, err
		}
	}
	if patch.RequestsAllowed != nil {
		policy.RequestsAllowed = *patch.RequestsAllowed
	}
	policy, err = normalizeResolvedPolicy(policy)
	if err != nil {
		return Policy{}, err
	}
	policy, err = resolveMaterializedPolicy(ctx, tx, policy)
	if err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func patchIntegerSet(current []int, patch IntegerSetPatch) ([]int, error) {
	for _, value := range patch.Values {
		if value <= 0 {
			return nil, fmt.Errorf("%w: library ids must be positive", ErrInvalidPolicy)
		}
	}
	switch strings.TrimSpace(patch.Mode) {
	case PolicySetAdd:
		return append(append([]int{}, current...), patch.Values...), nil
	case PolicySetRemove:
		removed := make(map[int]struct{}, len(patch.Values))
		for _, value := range patch.Values {
			removed[value] = struct{}{}
		}
		result := make([]int, 0, len(current))
		for _, value := range current {
			if _, ok := removed[value]; !ok {
				result = append(result, value)
			}
		}
		return result, nil
	case PolicySetReplace:
		return append([]int{}, patch.Values...), nil
	case PolicyLibrariesAll:
		return nil, nil
	case PolicyLibrariesNone:
		return []int{}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported library patch mode", ErrInvalidPolicy)
	}
}

func patchStringSet(current []string, patch StringSetPatch) ([]string, error) {
	switch strings.TrimSpace(patch.Mode) {
	case PolicySetAdd:
		if current == nil {
			return nil, nil
		}
		return append(append([]string{}, current...), patch.Values...), nil
	case PolicySetRemove:
		if current == nil {
			current = permissioncatalog.Assignable()
		}
		removed := make(map[string]struct{}, len(patch.Values))
		for _, value := range patch.Values {
			removed[strings.TrimSpace(value)] = struct{}{}
		}
		result := make([]string, 0, len(current))
		for _, value := range current {
			if _, ok := removed[value]; !ok {
				result = append(result, value)
			}
		}
		return result, nil
	case PolicySetReplace:
		return append([]string{}, patch.Values...), nil
	case PolicySetUnrestricted:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unsupported permission patch mode", ErrInvalidPolicy)
	}
}

func cohortPolicyDigest(policy Policy) (string, error) {
	policy, err := normalizeResolvedPolicy(policy)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("entitlements: encode cohort policy digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

const cohortSelect = `
	SELECT r.id,r.organization_id,r.name,r.revision,r.access_group_id,
	       r.source_template_key,r.source_template_revision,r.parent_id,
	       r.derivation_kind,r.library_ids,r.playback_allowed,r.max_streams,
	       r.max_profiles,r.transcode_allowed,r.max_transcodes,r.download_allowed,
	       r.download_transcode_allowed,r.max_playback_quality,
	       r.allowed_permissions,r.requests_allowed,r.policy_digest,c.archived,
	       r.created_by_account_id,r.created_at
	FROM entitlement_policy_cohort_revisions r
	JOIN entitlement_policy_cohorts c
	  ON c.id=r.cohort_id AND c.organization_id=r.organization_id`

type cohortQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getCohort(ctx context.Context, db cohortQuerier, organizationID, cohortID uuid.UUID) (CohortRevision, error) {
	item, err := scanCohort(db.QueryRow(ctx, cohortSelect+`
		WHERE r.organization_id=$1 AND r.id=$2`, organizationID, cohortID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CohortRevision{}, ErrCohortNotFound
	}
	if err != nil {
		return CohortRevision{}, fmt.Errorf("entitlements: load cohort: %w", err)
	}
	return item, nil
}

func getExactCohort(ctx context.Context, db cohortQuerier, organizationID uuid.UUID, key string, revision int64) (CohortRevision, bool, error) {
	item, err := scanCohort(db.QueryRow(ctx, cohortSelect+`
		WHERE r.organization_id=$1
		  AND r.source_template_key=$2
		  AND r.source_template_revision=$3
		  AND r.derivation_kind='exact_template'`, organizationID, key, revision))
	if errors.Is(err, pgx.ErrNoRows) {
		return CohortRevision{}, false, nil
	}
	if err != nil {
		return CohortRevision{}, false, fmt.Errorf("entitlements: load exact cohort: %w", err)
	}
	return item, true, nil
}

func getDerivedCohort(ctx context.Context, db cohortQuerier, organizationID, parentID uuid.UUID, name, digest string) (CohortRevision, bool, error) {
	item, err := scanCohort(db.QueryRow(ctx, cohortSelect+`
		WHERE r.organization_id=$1
		  AND r.parent_id=$2
		  AND lower(r.name)=lower($3)
		  AND r.policy_digest=$4
		  AND r.derivation_kind='policy_patch'`, organizationID, parentID, name, digest))
	if errors.Is(err, pgx.ErrNoRows) {
		return CohortRevision{}, false, nil
	}
	if err != nil {
		return CohortRevision{}, false, fmt.Errorf("entitlements: load derived cohort: %w", err)
	}
	return item, true, nil
}

func scanCohort(row rowScanner) (CohortRevision, error) {
	var item CohortRevision
	var parentID *uuid.UUID
	err := row.Scan(
		&item.ID, &item.OrganizationID, &item.Name, &item.Revision,
		&item.AccessGroupID, &item.SourceTemplateKey, &item.SourceTemplateRevision,
		&parentID, &item.DerivationKind, &item.Policy.LibraryIDs,
		&item.Policy.PlaybackAllowed, &item.Policy.MaxStreams, &item.Policy.MaxProfiles,
		&item.Policy.TranscodeAllowed, &item.Policy.MaxTranscodes,
		&item.Policy.DownloadAllowed, &item.Policy.DownloadTranscodeAllowed,
		&item.Policy.MaxPlaybackQuality, &item.Policy.AllowedPermissions,
		&item.Policy.RequestsAllowed, &item.PolicyDigest, &item.Archived,
		&item.CreatedByAccountID, &item.CreatedAt,
	)
	if parentID != nil {
		item.ParentID = *parentID
	}
	return item, err
}

func normalizeResolvedPolicy(policy Policy) (Policy, error) {
	librariesSpecified := policy.LibraryIDs != nil
	permissionsSpecified := policy.AllowedPermissions != nil
	normalized, err := normalizePolicy(policy)
	if err != nil {
		return Policy{}, err
	}
	if librariesSpecified && normalized.LibraryIDs == nil {
		normalized.LibraryIDs = []int{}
	}
	if permissionsSpecified && normalized.AllowedPermissions == nil {
		normalized.AllowedPermissions = []string{}
	}
	return normalized, nil
}
