package adminpeople

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	PolicyAssignEntitlementCohort   = "assign_entitlement_cohort"
	PolicyApplyEntitlementTemplate  = "apply_entitlement_template"
	PolicyDeriveEntitlementCohort   = "derive_entitlement_cohort"
	PolicyRestoreDefaultEntitlement = "restore_default_entitlement"
	policyConfirmationVersion       = 1
)

var (
	ErrInvalidPolicyCommand      = errors.New("invalid people policy command")
	ErrInvalidPolicyConfirmation = errors.New("invalid or stale people policy confirmation")
)

type PolicyCommand struct {
	Kind                  string                   `json:"kind"`
	CohortID              uuid.UUID                `json:"cohort_id,omitempty"`
	TemplateKey           string                   `json:"template_key,omitempty"`
	TemplateRevision      int64                    `json:"template_revision,omitempty"`
	Name                  string                   `json:"name,omitempty"`
	Patch                 entitlements.PolicyPatch `json:"patch,omitempty"`
	IncludeCustomProfiles bool                     `json:"include_custom_profiles"`
}

// PolicyView is the safe snake-case policy projection used by previews.
type PolicyView struct {
	LibraryIDs               []int    `json:"library_ids"`
	PlaybackAllowed          bool     `json:"playback_allowed"`
	MaxStreams               int      `json:"max_streams"`
	MaxProfiles              int      `json:"max_profiles"`
	TranscodeAllowed         bool     `json:"transcode_allowed"`
	MaxTranscodes            int      `json:"max_transcodes"`
	DownloadAllowed          bool     `json:"download_allowed"`
	DownloadTranscodeAllowed bool     `json:"download_transcode_allowed"`
	MaxPlaybackQuality       string   `json:"max_playback_quality"`
	AllowedPermissions       []string `json:"allowed_permissions"`
	RequestsAllowed          bool     `json:"requests_allowed"`
}

type PolicyTarget struct {
	Kind             string     `json:"kind"`
	CohortID         uuid.UUID  `json:"cohort_id,omitempty"`
	CohortRevision   int64      `json:"cohort_revision,omitempty"`
	ParentCohortID   uuid.UUID  `json:"parent_cohort_id,omitempty"`
	GroupID          int64      `json:"group_id,omitempty"`
	TemplateKey      string     `json:"template_key,omitempty"`
	TemplateRevision int64      `json:"template_revision,omitempty"`
	Name             string     `json:"name,omitempty"`
	PolicyDigest     string     `json:"policy_digest"`
	Policy           PolicyView `json:"policy"`
	policy           entitlements.Policy
}

type CohortDistribution struct {
	GroupID                int64     `json:"group_id,omitempty"`
	CohortID               uuid.UUID `json:"cohort_id,omitempty"`
	CohortRevision         int64     `json:"cohort_revision,omitempty"`
	SourceTemplateKey      string    `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64     `json:"source_template_revision,omitempty"`
	State                  string    `json:"state"`
	Count                  int64     `json:"count"`
}

type PolicyFieldDiff struct {
	Field           string `json:"field"`
	ChangedAccounts int64  `json:"changed_accounts"`
}

type PolicyPreview struct {
	Matched                   int64                `json:"matched"`
	Excluded                  int64                `json:"excluded"`
	AlreadyCompliant          int64                `json:"already_compliant"`
	InheritedProfilesWillMove int64                `json:"inherited_profiles_will_move"`
	CustomProfilesWillRemain  int64                `json:"custom_profiles_will_remain"`
	CustomProfilesWillMove    int64                `json:"custom_profiles_will_move"`
	IneligibleOrStale         int64                `json:"ineligible_or_stale"`
	CurrentCohorts            []CohortDistribution `json:"current_cohorts"`
	Target                    PolicyTarget         `json:"target"`
	Diff                      []PolicyFieldDiff    `json:"diff"`
	SelectionExpiresAt        time.Time            `json:"selection_expires_at"`
	ConfirmationExpiresAt     time.Time            `json:"confirmation_expires_at"`
	ConfirmationToken         string               `json:"confirmation_token"`
}

// PolicyConfirmation is the verified immutable input Task 4 persists with a
// durable policy job. It intentionally contains digests and revisions rather
// than an untrusted copy of request JSON.
type PolicyConfirmation struct {
	SelectionID                uuid.UUID
	CommandDigest              string
	TargetPolicyDigest         string
	Actor                      MutationActor
	OrganizationID             uuid.UUID
	OrganizationPolicyRevision int64
	ExpiresAt                  time.Time
}

type policyConfirmationPayload struct {
	Version                    int                 `json:"version"`
	SelectionID                uuid.UUID           `json:"selection_id"`
	SelectionDigest            string              `json:"selection_digest"`
	ObservationDigest          string              `json:"observation_digest"`
	CommandDigest              string              `json:"command_digest"`
	Target                     policyTargetBinding `json:"target"`
	ActorID                    int                 `json:"actor_id"`
	ActorAuthority             string              `json:"actor_authority"`
	ActorMembershipID          uuid.UUID           `json:"actor_membership_id,omitempty"`
	ActorSecurityRevision      int64               `json:"actor_security_revision"`
	OrganizationID             uuid.UUID           `json:"organization_id"`
	OrganizationPolicyRevision int64               `json:"organization_policy_revision"`
	ExpiresAt                  time.Time           `json:"expires_at"`
}

type policyTargetBinding struct {
	Kind             string    `json:"kind"`
	CohortID         uuid.UUID `json:"cohort_id,omitempty"`
	CohortRevision   int64     `json:"cohort_revision,omitempty"`
	ParentCohortID   uuid.UUID `json:"parent_cohort_id,omitempty"`
	GroupID          int64     `json:"group_id,omitempty"`
	TemplateKey      string    `json:"template_key,omitempty"`
	TemplateRevision int64     `json:"template_revision,omitempty"`
	PolicyDigest     string    `json:"policy_digest"`
}

type policyCommandBinding struct {
	Kind                  string    `json:"kind"`
	CohortID              uuid.UUID `json:"cohort_id,omitempty"`
	TemplateKey           string    `json:"template_key,omitempty"`
	TemplateRevision      int64     `json:"template_revision,omitempty"`
	Name                  string    `json:"name,omitempty"`
	PatchDigest           string    `json:"patch_digest"`
	IncludeCustomProfiles bool      `json:"include_custom_profiles"`
}

func (s *Service) PreviewPolicy(ctx context.Context, organizationID uuid.UUID, actorID int, selectionToken string, command PolicyCommand) (PolicyPreview, error) {
	if s == nil || s.pool == nil || organizationID == uuid.Nil || actorID <= 0 {
		return PolicyPreview{}, ErrInvalidPolicyCommand
	}
	reference, err := s.parseSelectionReference(selectionToken)
	if err != nil {
		return PolicyPreview{}, err
	}
	record, err := s.loadActivePolicySelection(ctx, organizationID, reference)
	if err != nil {
		return PolicyPreview{}, err
	}
	actor, err := mutationActor(ctx, actorID)
	if err != nil {
		return PolicyPreview{}, ErrInvalidPolicyCommand
	}
	actor, err = s.currentPolicyActor(ctx, organizationID, actor)
	if err != nil {
		return PolicyPreview{}, err
	}
	commandDigest, err := PolicyCommandDigest(command)
	if err != nil {
		return PolicyPreview{}, err
	}
	target, err := s.resolvePolicyTarget(ctx, organizationID, command)
	if err != nil {
		return PolicyPreview{}, err
	}

	preview := PolicyPreview{
		Matched: record.matched, Excluded: record.excluded,
		CurrentCohorts: []CohortDistribution{}, Target: target, Diff: []PolicyFieldDiff{},
		SelectionExpiresAt: record.expires, ConfirmationExpiresAt: record.expires,
	}
	preview.CurrentCohorts = currentCohortDistribution(record.targets)
	accountIDs := make([]int, len(record.targets))
	for index := range record.targets {
		accountIDs[index] = record.targets[index].AccountID
	}
	policyResults, _, err := entitlements.NewTemplateStore(s.pool).GetAccountPolicies(ctx, organizationID, accountIDs)
	if err != nil {
		return PolicyPreview{}, fmt.Errorf("preview account policies: %w", err)
	}
	results := make(map[int]entitlements.AccountPolicySnapshotResult, len(policyResults))
	for _, result := range policyResults {
		results[result.AccountID] = result
	}
	memberships, err := s.currentPolicyMemberships(ctx, organizationID, accountIDs)
	if err != nil {
		return PolicyPreview{}, err
	}
	observationDigest, err := policyObservationDigest(record.targets, results, memberships)
	if err != nil {
		return PolicyPreview{}, err
	}
	diffCounts := map[string]int64{}
	for _, snapshot := range record.targets {
		result, ok := results[snapshot.AccountID]
		membership, membershipOK := memberships[snapshot.AccountID]
		if !ok || result.Snapshot == nil || result.Error != "" || !membershipOK || snapshot.MembershipStatus != tenancy.MembershipActive || membership.id != snapshot.MembershipID || membership.status != snapshot.MembershipStatus || membership.revision != snapshot.MembershipRevision || result.Snapshot.GroupID != snapshot.GroupID || result.Snapshot.PolicyRevision != snapshot.AccountPolicyRevision || !samePolicyProfileSnapshots(snapshot.Profiles, result.Snapshot.Profiles) {
			preview.IneligibleOrStale++
			continue
		}
		current := policyFromEffective(result.Snapshot.Policy)
		already := (target.GroupID > 0 && snapshot.GroupID == target.GroupID) || entitlements.PolicyEqual(current, target.policy)
		if already {
			preview.AlreadyCompliant++
		}
		accountWillMove := target.GroupID == 0 || snapshot.GroupID != target.GroupID
		for _, profile := range snapshot.Profiles {
			if profile.InheritsAccount {
				if accountWillMove {
					preview.InheritedProfilesWillMove++
				}
			} else if command.IncludeCustomProfiles {
				if target.GroupID == 0 || int64(profile.GroupID) != target.GroupID {
					preview.CustomProfilesWillMove++
				}
			} else {
				preview.CustomProfilesWillRemain++
			}
		}
		accumulatePolicyDiff(diffCounts, current, target.policy)
	}
	fields := make([]string, 0, len(diffCounts))
	for field, count := range diffCounts {
		if count > 0 {
			fields = append(fields, field)
		}
	}
	sort.Strings(fields)
	for _, field := range fields {
		preview.Diff = append(preview.Diff, PolicyFieldDiff{Field: field, ChangedAccounts: diffCounts[field]})
	}

	selectionDigest, err := selectionPolicyDigest(record.targets)
	if err != nil {
		return PolicyPreview{}, err
	}
	payload := policyConfirmationPayload{
		Version: policyConfirmationVersion, SelectionID: reference, SelectionDigest: selectionDigest,
		ObservationDigest: observationDigest, CommandDigest: commandDigest, Target: target.binding(), ActorID: actor.AccountID,
		ActorAuthority: actor.Authority, ActorMembershipID: actor.MembershipID,
		ActorSecurityRevision: actor.SecurityRevision, OrganizationID: organizationID,
		OrganizationPolicyRevision: actor.PolicyRevision, ExpiresAt: record.expires,
	}
	preview.ConfirmationToken, err = s.signPolicyConfirmation(payload)
	if err != nil {
		return PolicyPreview{}, err
	}
	return preview, nil
}

type policySelectionMembership struct {
	id       uuid.UUID
	status   tenancy.MembershipStatus
	revision int64
}

type policyObservation struct {
	AccountID          int                       `json:"account_id"`
	MembershipFound    bool                      `json:"membership_found"`
	MembershipID       uuid.UUID                 `json:"membership_id,omitempty"`
	MembershipStatus   tenancy.MembershipStatus  `json:"membership_status,omitempty"`
	MembershipRevision int64                     `json:"membership_revision,omitempty"`
	ResultError        string                    `json:"result_error,omitempty"`
	Snapshot           *policyAccountObservation `json:"snapshot,omitempty"`
}

type policyAccountObservation struct {
	OrganizationID         uuid.UUID                            `json:"organization_id"`
	AccountID              int                                  `json:"account_id"`
	GroupID                int64                                `json:"group_id"`
	CohortID               uuid.UUID                            `json:"cohort_id,omitempty"`
	CohortRevision         int64                                `json:"cohort_revision,omitempty"`
	SourceTemplateKey      string                               `json:"source_template_key,omitempty"`
	SourceTemplateRevision int64                                `json:"source_template_revision,omitempty"`
	State                  string                               `json:"state"`
	PolicyRevision         int64                                `json:"policy_revision"`
	Policy                 entitlements.EffectivePolicySnapshot `json:"policy"`
	Profiles               []policyProfileObservation           `json:"profiles"`
}

type policyProfileObservation struct {
	ProfileID       string                               `json:"profile_id"`
	GroupID         int64                                `json:"group_id"`
	InheritsAccount bool                                 `json:"inherits_account"`
	State           string                               `json:"state"`
	Policy          entitlements.EffectivePolicySnapshot `json:"policy"`
}

func (s *Service) currentPolicyMemberships(ctx context.Context, organizationID uuid.UUID, accountIDs []int) (map[int]policySelectionMembership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT account_id,id,status,security_revision
		FROM organization_memberships
		WHERE organization_id=$1 AND account_id=ANY($2::integer[])`, organizationID, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("preview current memberships: %w", err)
	}
	defer rows.Close()
	result := make(map[int]policySelectionMembership, len(accountIDs))
	for rows.Next() {
		var accountID int
		var membership policySelectionMembership
		if err := rows.Scan(&accountID, &membership.id, &membership.status, &membership.revision); err != nil {
			return nil, fmt.Errorf("scan preview membership: %w", err)
		}
		result[accountID] = membership
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preview memberships: %w", err)
	}
	return result, nil
}

func samePolicyProfileSnapshots(selected []profileSnapshot, current []entitlements.ProfilePolicySnapshot) bool {
	if len(selected) != len(current) {
		return false
	}
	byID := make(map[string]entitlements.ProfilePolicySnapshot, len(current))
	for _, profile := range current {
		byID[profile.ProfileID] = profile
	}
	for _, profile := range selected {
		currentProfile, ok := byID[profile.ID]
		if !ok || currentProfile.GroupID != int64(profile.GroupID) || currentProfile.InheritsAccount != profile.InheritsAccount {
			return false
		}
	}
	return true
}

func policyObservationDigest(targets []targetSnapshot, results map[int]entitlements.AccountPolicySnapshotResult, memberships map[int]policySelectionMembership) (string, error) {
	observations := make([]policyObservation, 0, len(targets))
	for _, target := range targets {
		observation := policyObservation{AccountID: target.AccountID}
		if membership, ok := memberships[target.AccountID]; ok {
			observation.MembershipFound = true
			observation.MembershipID = membership.id
			observation.MembershipStatus = membership.status
			observation.MembershipRevision = membership.revision
		}
		if result, ok := results[target.AccountID]; ok {
			observation.ResultError = result.Error
			if result.Snapshot != nil {
				snapshot := result.Snapshot
				profiles := make([]policyProfileObservation, 0, len(snapshot.Profiles))
				for _, profile := range snapshot.Profiles {
					profiles = append(profiles, policyProfileObservation{
						ProfileID: profile.ProfileID, GroupID: profile.GroupID,
						InheritsAccount: profile.InheritsAccount, State: profile.State, Policy: profile.Policy,
					})
				}
				sort.Slice(profiles, func(i, j int) bool { return profiles[i].ProfileID < profiles[j].ProfileID })
				observation.Snapshot = &policyAccountObservation{
					OrganizationID: snapshot.OrganizationID, AccountID: snapshot.AccountID,
					GroupID: snapshot.GroupID, CohortID: snapshot.CohortID, CohortRevision: snapshot.CohortRevision,
					SourceTemplateKey: snapshot.SourceTemplateKey, SourceTemplateRevision: snapshot.SourceTemplateRevision,
					State: snapshot.State, PolicyRevision: snapshot.PolicyRevision, Policy: snapshot.Policy, Profiles: profiles,
				}
			}
		} else {
			observation.ResultError = entitlements.AccountPolicyResultNotFound
		}
		observations = append(observations, observation)
	}
	payload, err := json.Marshal(observations)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func (s *Service) ValidatePolicyConfirmation(ctx context.Context, organizationID uuid.UUID, actorID int, selectionToken string, command PolicyCommand, token string) (PolicyConfirmation, error) {
	invalid := func() (PolicyConfirmation, error) { return PolicyConfirmation{}, ErrInvalidPolicyConfirmation }
	if s == nil || s.pool == nil || organizationID == uuid.Nil || actorID <= 0 {
		return invalid()
	}
	payload, err := s.parsePolicyConfirmation(token)
	if err != nil || payload.Version != policyConfirmationVersion || !s.now().UTC().Before(payload.ExpiresAt) {
		return invalid()
	}
	reference, err := s.parseSelectionReference(selectionToken)
	if err != nil || reference != payload.SelectionID {
		return invalid()
	}
	record, err := s.loadActivePolicySelection(ctx, organizationID, reference)
	if err != nil {
		return invalid()
	}
	selectionDigest, err := selectionPolicyDigest(record.targets)
	if err != nil || !hmac.Equal([]byte(selectionDigest), []byte(payload.SelectionDigest)) {
		return invalid()
	}
	actor, err := mutationActor(ctx, actorID)
	if err != nil {
		return invalid()
	}
	actor, err = s.currentPolicyActor(ctx, organizationID, actor)
	if err != nil {
		return invalid()
	}
	commandDigest, err := PolicyCommandDigest(command)
	if err != nil {
		return invalid()
	}
	target, err := s.resolvePolicyTarget(ctx, organizationID, command)
	if err != nil {
		return invalid()
	}
	accountIDs := make([]int, len(record.targets))
	for index := range record.targets {
		accountIDs[index] = record.targets[index].AccountID
	}
	policyResults, _, err := entitlements.NewTemplateStore(s.pool).GetAccountPolicies(ctx, organizationID, accountIDs)
	if err != nil {
		return invalid()
	}
	results := make(map[int]entitlements.AccountPolicySnapshotResult, len(policyResults))
	for _, result := range policyResults {
		results[result.AccountID] = result
	}
	memberships, err := s.currentPolicyMemberships(ctx, organizationID, accountIDs)
	if err != nil {
		return invalid()
	}
	observationDigest, err := policyObservationDigest(record.targets, results, memberships)
	if err != nil {
		return invalid()
	}
	if payload.OrganizationID != organizationID || payload.ActorID != actor.AccountID || payload.ActorAuthority != actor.Authority || payload.ActorMembershipID != actor.MembershipID || payload.ActorSecurityRevision != actor.SecurityRevision || payload.OrganizationPolicyRevision != actor.PolicyRevision || !hmac.Equal([]byte(payload.CommandDigest), []byte(commandDigest)) || !hmac.Equal([]byte(payload.ObservationDigest), []byte(observationDigest)) || !reflect.DeepEqual(payload.Target, target.binding()) || !payload.ExpiresAt.Equal(record.expires) {
		return invalid()
	}
	return PolicyConfirmation{
		SelectionID: reference, CommandDigest: commandDigest, TargetPolicyDigest: target.PolicyDigest,
		Actor: actor, OrganizationID: organizationID, OrganizationPolicyRevision: actor.PolicyRevision,
		ExpiresAt: payload.ExpiresAt,
	}, nil
}

func PolicyCommandDigest(command PolicyCommand) (string, error) {
	command.Kind = strings.TrimSpace(command.Kind)
	command.TemplateKey = strings.TrimSpace(command.TemplateKey)
	command.Name = strings.TrimSpace(command.Name)
	patchDigest, err := entitlements.PolicyPatchDigest(command.Patch)
	if err != nil {
		return "", ErrInvalidPolicyCommand
	}
	emptyPatch, err := isEmptyPolicyPatch(command.Patch)
	if err != nil {
		return "", ErrInvalidPolicyCommand
	}
	switch command.Kind {
	case PolicyAssignEntitlementCohort:
		if command.CohortID == uuid.Nil || command.TemplateKey != "" || command.TemplateRevision != 0 || command.Name != "" || !emptyPatch {
			return "", ErrInvalidPolicyCommand
		}
	case PolicyApplyEntitlementTemplate:
		if command.CohortID != uuid.Nil || command.TemplateKey == "" || command.TemplateRevision <= 0 || command.Name != "" || !emptyPatch {
			return "", ErrInvalidPolicyCommand
		}
	case PolicyDeriveEntitlementCohort:
		if command.CohortID == uuid.Nil || command.TemplateKey != "" || command.TemplateRevision != 0 || command.Name == "" || emptyPatch {
			return "", ErrInvalidPolicyCommand
		}
	case PolicyRestoreDefaultEntitlement:
		if command.CohortID != uuid.Nil || command.TemplateKey != "" || command.TemplateRevision != 0 || command.Name != "" || !emptyPatch {
			return "", ErrInvalidPolicyCommand
		}
	default:
		return "", ErrInvalidPolicyCommand
	}
	payload, err := json.Marshal(policyCommandBinding{
		Kind: command.Kind, CohortID: command.CohortID, TemplateKey: command.TemplateKey,
		TemplateRevision: command.TemplateRevision, Name: command.Name, PatchDigest: patchDigest,
		IncludeCustomProfiles: command.IncludeCustomProfiles,
	})
	if err != nil {
		return "", ErrInvalidPolicyCommand
	}
	return digestBytes(payload), nil
}

func (s *Service) loadActivePolicySelection(ctx context.Context, organizationID, reference uuid.UUID) (selectionRecord, error) {
	record, err := loadSelection(ctx, s.pool.QueryRow(ctx, `SELECT id,organization_id,canonical_filter,snapshot_at,expires_at,account_ids,matched_count,excluded_count,targets FROM admin_people_selections WHERE id=$1 AND organization_id=$2`, reference, organizationID), organizationID)
	if err != nil {
		return selectionRecord{}, err
	}
	if !s.now().UTC().Before(record.expires) {
		return selectionRecord{}, ErrSelectionExpired
	}
	return record, nil
}

func (s *Service) currentPolicyActor(ctx context.Context, organizationID uuid.UUID, actor MutationActor) (MutationActor, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return MutationActor{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actor, err = s.resolveBulkActorSnapshot(ctx, tx, organizationID, actor)
	if err != nil {
		return MutationActor{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return MutationActor{}, err
	}
	return actor, nil
}

func (s *Service) resolvePolicyTarget(ctx context.Context, organizationID uuid.UUID, command PolicyCommand) (PolicyTarget, error) {
	if _, err := PolicyCommandDigest(command); err != nil {
		return PolicyTarget{}, err
	}
	store := entitlements.NewTemplateStore(s.pool)
	var target PolicyTarget
	switch strings.TrimSpace(command.Kind) {
	case PolicyAssignEntitlementCohort:
		cohort, err := store.GetCohort(ctx, organizationID, command.CohortID)
		if err != nil || cohort.Archived {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		target = policyTargetFromCohort(PolicyAssignEntitlementCohort, cohort)
	case PolicyApplyEntitlementTemplate:
		template, err := store.Get(ctx, strings.TrimSpace(command.TemplateKey), command.TemplateRevision)
		if err != nil || !template.Enabled || template.Archived {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		policy, err := s.materializePreviewPolicy(ctx, template.Policy)
		if err != nil {
			return PolicyTarget{}, err
		}
		target = PolicyTarget{Kind: PolicyApplyEntitlementTemplate, TemplateKey: template.Key, TemplateRevision: template.Revision, Name: template.Name, policy: policy}
		cohorts, err := store.ListCohorts(ctx, organizationID, false)
		if err != nil {
			return PolicyTarget{}, err
		}
		for _, cohort := range cohorts {
			if cohort.DerivationKind == "exact_template" && cohort.SourceTemplateKey == template.Key && cohort.SourceTemplateRevision == template.Revision {
				target.CohortID, target.CohortRevision, target.GroupID = cohort.ID, cohort.Revision, cohort.AccessGroupID
				break
			}
		}
	case PolicyDeriveEntitlementCohort:
		parent, err := store.GetCohort(ctx, organizationID, command.CohortID)
		if err != nil || parent.Archived {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		policy, err := entitlements.ApplyPolicyPatch(parent.Policy, command.Patch)
		if err != nil {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		policy, err = s.materializePreviewPolicy(ctx, policy)
		if err != nil {
			return PolicyTarget{}, err
		}
		if entitlements.PolicyEqual(policy, parent.Policy) {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		target = PolicyTarget{
			Kind: PolicyDeriveEntitlementCohort, ParentCohortID: parent.ID,
			TemplateKey: parent.SourceTemplateKey, TemplateRevision: parent.SourceTemplateRevision,
			Name: strings.TrimSpace(command.Name), policy: policy,
		}
		candidateDigest, _ := entitlements.PolicyDigest(policy)
		cohorts, err := store.ListCohorts(ctx, organizationID, false)
		if err != nil {
			return PolicyTarget{}, err
		}
		for _, cohort := range cohorts {
			if cohort.ParentID == parent.ID && strings.EqualFold(cohort.Name, target.Name) && cohort.PolicyDigest == candidateDigest {
				target.CohortID, target.CohortRevision, target.GroupID = cohort.ID, cohort.Revision, cohort.AccessGroupID
				break
			}
		}
	case PolicyRestoreDefaultEntitlement:
		var cohortID uuid.UUID
		err := s.pool.QueryRow(ctx, `
			SELECT g.id,COALESCE(g.managed_cohort_id,'00000000-0000-0000-0000-000000000000'::uuid),
			       COALESCE(r.revision,0),COALESCE(r.source_template_key,''),COALESCE(r.source_template_revision,0),g.name,
			       g.library_ids,g.playback_allowed,g.max_streams,g.max_profiles,g.transcode_allowed,
			       g.max_transcodes,g.download_allowed,g.download_transcode_allowed,g.max_playback_quality,
			       g.allowed_permissions,g.requests_allowed
			FROM access_groups g
			LEFT JOIN entitlement_policy_cohort_revisions r ON r.organization_id=g.organization_id AND r.id=g.managed_cohort_id AND r.access_group_id=g.id
			WHERE g.organization_id=$1 AND g.is_default`, organizationID).Scan(
			&target.GroupID, &cohortID, &target.CohortRevision, &target.TemplateKey, &target.TemplateRevision, &target.Name,
			&target.policy.LibraryIDs, &target.policy.PlaybackAllowed, &target.policy.MaxStreams, &target.policy.MaxProfiles,
			&target.policy.TranscodeAllowed, &target.policy.MaxTranscodes, &target.policy.DownloadAllowed,
			&target.policy.DownloadTranscodeAllowed, &target.policy.MaxPlaybackQuality,
			&target.policy.AllowedPermissions, &target.policy.RequestsAllowed,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return PolicyTarget{}, ErrInvalidPolicyCommand
		}
		if err != nil {
			return PolicyTarget{}, err
		}
		target.Kind, target.CohortID = PolicyRestoreDefaultEntitlement, cohortID
	}
	target.PolicyDigest, _ = entitlements.PolicyDigest(target.policy)
	target.Policy = policyView(target.policy)
	return target, nil
}

func (s *Service) materializePreviewPolicy(ctx context.Context, policy entitlements.Policy) (entitlements.Policy, error) {
	if policy.LibraryIDs != nil {
		return entitlements.ApplyPolicyPatch(policy, entitlements.PolicyPatch{})
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM media_folders WHERE enabled ORDER BY id`)
	if err != nil {
		return entitlements.Policy{}, err
	}
	defer rows.Close()
	policy.LibraryIDs = []int{}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return entitlements.Policy{}, err
		}
		policy.LibraryIDs = append(policy.LibraryIDs, id)
	}
	if err := rows.Err(); err != nil {
		return entitlements.Policy{}, err
	}
	return entitlements.ApplyPolicyPatch(policy, entitlements.PolicyPatch{})
}

func policyTargetFromCohort(kind string, cohort entitlements.CohortRevision) PolicyTarget {
	return PolicyTarget{
		Kind: kind, CohortID: cohort.ID, CohortRevision: cohort.Revision, ParentCohortID: cohort.ParentID,
		GroupID: cohort.AccessGroupID, TemplateKey: cohort.SourceTemplateKey,
		TemplateRevision: cohort.SourceTemplateRevision, Name: cohort.Name,
		PolicyDigest: cohort.PolicyDigest, Policy: policyView(cohort.Policy), policy: cohort.Policy,
	}
}

func (target PolicyTarget) binding() policyTargetBinding {
	binding := policyTargetBinding{
		Kind: target.Kind, TemplateKey: target.TemplateKey,
		TemplateRevision: target.TemplateRevision, PolicyDigest: target.PolicyDigest,
	}
	switch target.Kind {
	case PolicyAssignEntitlementCohort, PolicyRestoreDefaultEntitlement:
		binding.CohortID = target.CohortID
		binding.CohortRevision = target.CohortRevision
		binding.GroupID = target.GroupID
	case PolicyDeriveEntitlementCohort:
		binding.ParentCohortID = target.ParentCohortID
	}
	return binding
}

func currentCohortDistribution(targets []targetSnapshot) []CohortDistribution {
	type key struct {
		groupID          int64
		cohortID         uuid.UUID
		cohortRevision   int64
		templateKey      string
		templateRevision int64
	}
	counts := map[key]int64{}
	for _, target := range targets {
		counts[key{target.GroupID, target.CohortID, target.CohortRevision, target.SourceTemplateKey, target.SourceTemplateRevision}]++
	}
	result := make([]CohortDistribution, 0, len(counts))
	for item, count := range counts {
		state := entitlements.AccountPolicyStateManaged
		if item.cohortID == uuid.Nil {
			state = entitlements.AccountPolicyStateCustom
			if item.groupID == 0 {
				state = entitlements.AccountPolicyStateLegacyUnmanaged
			}
		}
		result = append(result, CohortDistribution{
			GroupID: item.groupID, CohortID: item.cohortID, CohortRevision: item.cohortRevision,
			SourceTemplateKey: item.templateKey, SourceTemplateRevision: item.templateRevision,
			State: state, Count: count,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].State != result[j].State {
			return result[i].State < result[j].State
		}
		if result[i].CohortID != result[j].CohortID {
			return result[i].CohortID.String() < result[j].CohortID.String()
		}
		return result[i].GroupID < result[j].GroupID
	})
	return result
}

func accumulatePolicyDiff(counts map[string]int64, current, target entitlements.Policy) {
	if !reflect.DeepEqual(current.LibraryIDs, target.LibraryIDs) {
		counts["library_ids"]++
	}
	if current.PlaybackAllowed != target.PlaybackAllowed {
		counts["playback_allowed"]++
	}
	if current.MaxStreams != target.MaxStreams {
		counts["max_streams"]++
	}
	if current.MaxProfiles != target.MaxProfiles {
		counts["max_profiles"]++
	}
	if current.TranscodeAllowed != target.TranscodeAllowed {
		counts["transcode_allowed"]++
	}
	if current.MaxTranscodes != target.MaxTranscodes {
		counts["max_transcodes"]++
	}
	if current.DownloadAllowed != target.DownloadAllowed {
		counts["download_allowed"]++
	}
	if current.DownloadTranscodeAllowed != target.DownloadTranscodeAllowed {
		counts["download_transcode_allowed"]++
	}
	if current.MaxPlaybackQuality != target.MaxPlaybackQuality {
		counts["max_playback_quality"]++
	}
	if !reflect.DeepEqual(current.AllowedPermissions, target.AllowedPermissions) {
		counts["allowed_permissions"]++
	}
	if current.RequestsAllowed != target.RequestsAllowed {
		counts["requests_allowed"]++
	}
}

func policyFromEffective(policy entitlements.EffectivePolicySnapshot) entitlements.Policy {
	return entitlements.Policy{
		LibraryIDs: policy.LibraryIDs, PlaybackAllowed: policy.PlaybackAllowed,
		MaxStreams: policy.MaxStreams, MaxProfiles: policy.MaxProfiles,
		TranscodeAllowed: policy.TranscodeAllowed, MaxTranscodes: policy.MaxTranscodes,
		DownloadAllowed: policy.DownloadAllowed, DownloadTranscodeAllowed: policy.DownloadTranscodeAllowed,
		MaxPlaybackQuality: policy.MaxPlaybackQuality, AllowedPermissions: policy.AllowedPermissions,
		RequestsAllowed: policy.RequestsAllowed,
	}
}

func policyView(policy entitlements.Policy) PolicyView {
	return PolicyView{
		LibraryIDs: policy.LibraryIDs, PlaybackAllowed: policy.PlaybackAllowed,
		MaxStreams: policy.MaxStreams, MaxProfiles: policy.MaxProfiles,
		TranscodeAllowed: policy.TranscodeAllowed, MaxTranscodes: policy.MaxTranscodes,
		DownloadAllowed: policy.DownloadAllowed, DownloadTranscodeAllowed: policy.DownloadTranscodeAllowed,
		MaxPlaybackQuality: policy.MaxPlaybackQuality, AllowedPermissions: policy.AllowedPermissions,
		RequestsAllowed: policy.RequestsAllowed,
	}
}

func selectionPolicyDigest(targets []targetSnapshot) (string, error) {
	payload, err := json.Marshal(targets)
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func isEmptyPolicyPatch(patch entitlements.PolicyPatch) (bool, error) {
	payload, err := json.Marshal(patch)
	return string(payload) == "{}", err
}

func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (s *Service) signPolicyConfirmation(payload policyConfirmationPayload) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte("policy-confirmation." + encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Service) parsePolicyConfirmation(token string) (policyConfirmationPayload, error) {
	var payload policyConfirmationPayload
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return payload, ErrInvalidPolicyConfirmation
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return payload, ErrInvalidPolicyConfirmation
	}
	mac := hmac.New(sha256.New, s.key[:])
	_, _ = mac.Write([]byte("policy-confirmation." + parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return payload, ErrInvalidPolicyConfirmation
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != parts[0] || json.Unmarshal(raw, &payload) != nil {
		return policyConfirmationPayload{}, ErrInvalidPolicyConfirmation
	}
	return payload, nil
}
