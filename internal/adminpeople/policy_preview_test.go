package adminpeople

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/google/uuid"
)

func TestPolicyPreviewUsesImmutableAuthoritativeSelectionSnapshot(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Preview custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Custom profile", customGroup)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2 AND id<>$4`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID, customProfile); err != nil {
		t.Fatal(err)
	}

	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reference, _ := fixture.service.parseSelectionReference(selection.Token)
	var targets []byte
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT targets FROM admin_people_selections WHERE id=$1`, reference).Scan(&targets); err != nil {
		t.Fatal(err)
	}
	var stored []targetSnapshot
	if err := json.Unmarshal(targets, &stored); err != nil {
		t.Fatal(err)
	}
	inherited, custom := 0, 0
	if len(stored) == 1 {
		for _, profile := range stored[0].Profiles {
			if profile.InheritsAccount {
				inherited++
			} else {
				custom++
			}
		}
	}
	if len(stored) != 1 || stored[0].GroupID != standard.AccessGroupID || stored[0].CohortID != standard.ID || stored[0].CohortRevision != standard.Revision || stored[0].AccountPolicyRevision != 1 || len(stored[0].Profiles) != 2 || inherited != 1 || custom != 1 {
		t.Fatalf("immutable targets omit authoritative policy state: %s", targets)
	}

	command := PolicyCommand{Kind: PolicyApplyEntitlementTemplate, TemplateKey: "premium", TemplateRevision: 1}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Matched != 1 || preview.Excluded != 0 || preview.AlreadyCompliant != 0 || preview.InheritedProfilesWillMove != 1 || preview.CustomProfilesWillRemain != 1 || preview.CustomProfilesWillMove != 0 || preview.ConfirmationToken == "" {
		t.Fatalf("preview counts = %+v", preview)
	}
	if len(preview.CurrentCohorts) != 1 || preview.CurrentCohorts[0].CohortID != standard.ID || preview.CurrentCohorts[0].Count != 1 {
		t.Fatalf("current cohorts = %+v", preview.CurrentCohorts)
	}
	if preview.Target.TemplateKey != "premium" || preview.Target.TemplateRevision != 1 || len(preview.Diff) == 0 {
		t.Fatalf("target/diff = %+v / %+v", preview.Target, preview.Diff)
	}

	command.IncludeCustomProfiles = true
	includeCustom, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if includeCustom.CustomProfilesWillMove != 1 || includeCustom.CustomProfilesWillRemain != 0 {
		t.Fatalf("custom profile impact = %+v", includeCustom)
	}

	already, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: standard.ID})
	if err != nil {
		t.Fatal(err)
	}
	if already.AlreadyCompliant != 1 || already.InheritedProfilesWillMove != 0 || already.CustomProfilesWillRemain != 1 {
		t.Fatalf("already-compliant impact = %+v", already)
	}
}

func TestPolicyPreviewCountsPolicyEquivalentAccountThatMustMoveCohorts(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	restricted, _, err := store.DeriveCohortInTx(fixture.ctx, tx, fixture.orgA, standard.ID, "Restricted equivalent", entitlements.PolicyPatch{
		Permissions: &entitlements.SetOperation[string]{Mode: entitlements.PolicySetReplace, Values: []string{}},
	}, fixture.ownerID)
	if err != nil {
		_ = tx.Rollback(fixture.ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	customGroup := fixture.addGroup(t, fixture.orgA, "Equivalent custom", false)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE access_groups AS target
		SET (library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
		     max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,
		     allowed_permissions,requests_allowed) =
		    (SELECT library_ids,playback_allowed,max_streams,max_profiles,transcode_allowed,
		            max_transcodes,download_allowed,download_transcode_allowed,max_playback_quality,
		            allowed_permissions,requests_allowed FROM access_groups WHERE id=$2)
		WHERE target.id=$1`, customGroup, restricted.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.sharedAccountID, customGroup); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2`, fixture.orgA, fixture.sharedAccountID, customGroup); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, PolicyCommand{Kind: PolicyAssignEntitlementCohort, CohortID: restricted.ID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Target.GroupID != restricted.AccessGroupID || preview.AlreadyCompliant != 1 || preview.InheritedProfilesWillMove != 1 {
		t.Fatalf("equivalent policy/new cohort preview = %+v", preview)
	}
}

func TestPolicyPreviewRejectsBlankAndNoOpDerivedCommands(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	for _, command := range []PolicyCommand{
		{Kind: PolicyDeriveEntitlementCohort, CohortID: standard.ID, Name: "   ", Patch: entitlements.PolicyPatch{MaxStreams: intPointer(2)}},
		{Kind: PolicyDeriveEntitlementCohort, CohortID: standard.ID, Name: "No change", Patch: entitlements.PolicyPatch{MaxStreams: intPointer(standard.Policy.MaxStreams)}},
	} {
		if _, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command); !errors.Is(err, ErrInvalidPolicyCommand) {
			t.Fatalf("command %+v error = %v, want ErrInvalidPolicyCommand", command, err)
		}
	}
}

func TestPolicyPreviewMarksMembershipAndProfileDriftStale(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	customGroup := fixture.addGroup(t, fixture.orgA, "Drift custom", false)
	customProfile := fixture.addProfile(t, fixture.sharedAccountID, fixture.orgA, "Drift profile", customGroup)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2 AND id<>$4`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID, customProfile); err != nil {
		t.Fatal(err)
	}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	command := PolicyCommand{Kind: PolicyApplyEntitlementTemplate, TemplateKey: "premium", TemplateRevision: 1}

	profileSelection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2 AND id=$4`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID, customProfile); err != nil {
		t.Fatal(err)
	}
	profilePreview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, profileSelection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if profilePreview.IneligibleOrStale != 1 {
		t.Fatalf("profile-drift preview = %+v", profilePreview)
	}

	membershipSelection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET security_revision=security_revision+1 WHERE organization_id=$1 AND account_id=$2`, fixture.orgA, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}
	membershipPreview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, membershipSelection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if membershipPreview.IneligibleOrStale != 1 {
		t.Fatalf("membership-drift preview = %+v", membershipPreview)
	}
}

func TestPolicyPreviewConfirmationRejectsEveryMaterialBindingChange(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	otherSelection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "owner@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyApplyEntitlementTemplate, TemplateKey: "premium", TemplateRevision: 1}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ValidatePolicyConfirmation(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, preview.ConfirmationToken); err != nil {
		t.Fatalf("original confirmation rejected: %v", err)
	}
	ensurePreviewCohort(t, fixture, store, "premium", 1)
	if _, err := fixture.service.ValidatePolicyConfirmation(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, preview.ConfirmationToken); err != nil {
		t.Fatalf("convergent exact-cohort creation invalidated confirmation: %v", err)
	}

	changedCommand := command
	changedCommand.IncludeCustomProfiles = true
	checks := []struct {
		name      string
		ctx       MutationActor
		actorID   int
		selection string
		command   PolicyCommand
	}{
		{name: "command", ctx: MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1}, actorID: fixture.ownerID, selection: selection.Token, command: changedCommand},
		{name: "selection", ctx: MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1}, actorID: fixture.ownerID, selection: otherSelection.Token, command: command},
		{name: "actor", ctx: MutationActor{AccountID: fixture.sharedAccountID, Authority: AuthorityOrganizationAdmin, MembershipID: uuid.New()}, actorID: fixture.sharedAccountID, selection: selection.Token, command: command},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			changedCtx := WithMutationActor(fixture.ctx, check.ctx)
			if _, err := fixture.service.ValidatePolicyConfirmation(changedCtx, fixture.orgA, check.actorID, check.selection, check.command, preview.ConfirmationToken); !errors.Is(err, ErrInvalidPolicyConfirmation) {
				t.Fatalf("error = %v, want ErrInvalidPolicyConfirmation", err)
			}
		})
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_memberships SET security_revision=security_revision+1 WHERE id=$1`, fixture.ownerMembershipID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ValidatePolicyConfirmation(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, preview.ConfirmationToken); !errors.Is(err, ErrInvalidPolicyConfirmation) {
		t.Fatalf("actor security-revision change error = %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organizations SET policy_revision=policy_revision+1 WHERE id=$1`, fixture.orgA); err != nil {
		t.Fatal(err)
	}
	updatedActorContext := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 2})
	if _, err := fixture.service.ValidatePolicyConfirmation(updatedActorContext, fixture.orgA, fixture.ownerID, selection.Token, command, preview.ConfirmationToken); !errors.Is(err, ErrInvalidPolicyConfirmation) {
		t.Fatalf("policy-revision change error = %v", err)
	}
}

func TestPolicyPreviewConfirmationInvalidatesOnObservedPolicyRevisionChange(t *testing.T) {
	fixture := newPeopleFixture(t)
	store := entitlements.NewTemplateStore(fixture.pool)
	standard := ensurePreviewCohort(t, fixture, store, "standard", 1)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_group_id=$2 WHERE id=$1`, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE user_profiles SET access_group_id=$3 WHERE organization_id=$1 AND user_id=$2`, fixture.orgA, fixture.sharedAccountID, standard.AccessGroupID); err != nil {
		t.Fatal(err)
	}
	selection, err := fixture.service.CreateSelection(fixture.ctx, fixture.orgA, Filter{Query: "shared@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	command := PolicyCommand{Kind: PolicyApplyEntitlementTemplate, TemplateKey: "premium", TemplateRevision: 1}
	ctx := WithMutationActor(fixture.ctx, MutationActor{AccountID: fixture.ownerID, Authority: AuthorityOrganizationAdmin, MembershipID: fixture.ownerMembershipID, SecurityRevision: 1})
	preview, err := fixture.service.PreviewPolicy(ctx, fixture.orgA, fixture.ownerID, selection.Token, command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET access_policy_revision=access_policy_revision+1 WHERE id=$1`, fixture.sharedAccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.ValidatePolicyConfirmation(ctx, fixture.orgA, fixture.ownerID, selection.Token, command, preview.ConfirmationToken); !errors.Is(err, ErrInvalidPolicyConfirmation) {
		t.Fatalf("account policy-revision change error = %v, want ErrInvalidPolicyConfirmation", err)
	}
}

func ensurePreviewCohort(t *testing.T, fixture *peopleFixture, store *entitlements.Store, key string, revision int64) entitlements.CohortRevision {
	t.Helper()
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	cohort, _, err := store.EnsureExactCohortInTx(fixture.ctx, tx, fixture.orgA, key, revision, fixture.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	return cohort
}

func intPointer(value int) *int { return &value }
