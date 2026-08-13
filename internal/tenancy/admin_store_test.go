package tenancy_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestAdminStoreCreateOrganizationCreatesOwnerMembership(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{
		Name: "North Sea Media", Slug: "north-sea-media", OwnerAccountID: fixture.otherID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != OrganizationActive || created.PolicyRevision != 1 || created.OwnerAccountID == nil || *created.OwnerAccountID != fixture.otherID {
		t.Fatalf("organization = %+v", created)
	}
	membership, err := store.GetMembership(fixture.ctx, fixture.otherID, created.ID)
	if err != nil || membership.LegacyRole != "admin" || membership.Status != MembershipActive {
		t.Fatalf("membership = %+v, err = %v", membership, err)
	}
	summary, err := store.GetOrganizationSummary(fixture.ctx, created.ID)
	if err != nil || summary.MembershipCount != 1 || summary.ProfileCount != 0 || summary.LibraryCount != 0 || summary.EntitlementCount != 0 {
		t.Fatalf("organization summary = %+v, err = %v", summary, err)
	}

	_, err = store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{
		Name: "Duplicate", Slug: "NORTH-SEA-MEDIA", OwnerAccountID: fixture.adminID,
	})
	if !errors.Is(err, ErrOrganizationSlugConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrOrganizationSlugConflict", err)
	}
}

func TestAdminStoreListOrganizationsUsesStableCursorAndExactCounts(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	for _, input := range []CreateOrganizationInput{
		{Name: "alpha", Slug: "alpha", OwnerAccountID: fixture.adminID},
		{Name: "Alpha", Slug: "alpha-two", OwnerAccountID: fixture.otherID},
		{Name: "Zulu", Slug: "zulu", OwnerAccountID: fixture.adminID},
	} {
		if _, err := store.CreateOrganization(adminMutationContext(fixture), input); err != nil {
			t.Fatalf("CreateOrganization(%q): %v", input.Name, err)
		}
	}

	first, err := store.ListOrganizations(fixture.ctx, OrganizationFilter{Query: "alpha", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := store.ListOrganizations(fixture.ctx, OrganizationFilter{Query: "alpha", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID || second.NextCursor != "" {
		t.Fatalf("second page = %+v after %+v", second, first)
	}
	for _, item := range append(first.Items, second.Items...) {
		if item.MembershipCount != 1 || item.ProfileCount != 0 || item.LibraryCount != 0 || item.EntitlementCount != 0 {
			t.Fatalf("counts for %s = memberships %d profiles %d libraries %d entitlements %d", item.ID, item.MembershipCount, item.ProfileCount, item.LibraryCount, item.EntitlementCount)
		}
	}
}

func TestAdminStoreUpdateAndSuspensionAreRevisionGuardedAndReversible(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "North Sea", Slug: "north-sea", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	name := "North Sea Media"
	updated, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.PolicyRevision != created.PolicyRevision+1 {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name}); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale update error = %v, want ErrAuthorizationStateChanged", err)
	}
	suspended, err := store.SetOrganizationStatus(adminMutationContext(fixture), created.ID, updated.PolicyRevision, OrganizationSuspended)
	if err != nil {
		t.Fatal(err)
	}
	reactivated, err := store.SetOrganizationStatus(adminMutationContext(fixture), created.ID, suspended.PolicyRevision, OrganizationActive)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.Status != OrganizationSuspended || reactivated.Status != OrganizationActive || reactivated.PolicyRevision != suspended.PolicyRevision+1 {
		t.Fatalf("suspended/reactivated = %+v / %+v", suspended, reactivated)
	}
}

func TestAdminStoreMembershipLifecycleIsOrganizationBounded(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	first, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "First", Slug: "first", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Second", Slug: "second", OwnerAccountID: fixture.otherID})
	if err != nil {
		t.Fatal(err)
	}
	memberAccountID := fixture.insertAccount(t, "member", "user")
	membership, organization, err := store.CreateMembership(adminMutationContext(fixture), first.ID, first.PolicyRevision, CreateMembershipInput{AccountID: memberAccountID, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if membership.OrganizationID != first.ID || membership.Status != MembershipActive || organization.PolicyRevision != first.PolicyRevision+1 {
		t.Fatalf("membership/organization = %+v / %+v", membership, organization)
	}
	loaded, err := store.GetOrganizationMembership(fixture.ctx, first.ID, membership.ID)
	if err != nil || loaded != membership {
		t.Fatalf("GetOrganizationMembership = %+v, %v, want %+v", loaded, err, membership)
	}
	if _, err := store.GetOrganizationMembership(fixture.ctx, second.ID, membership.ID); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("cross-organization membership read error = %v, want ErrMembershipNotFound", err)
	}

	if _, _, err := store.UpdateMembership(adminMutationContext(fixture), second.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipSuspended)}); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("cross-organization update error = %v, want ErrMembershipNotFound", err)
	}
	updated, _, err := store.UpdateMembership(adminMutationContext(fixture), first.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipSuspended)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != MembershipSuspended || updated.SecurityRevision != membership.SecurityRevision+1 {
		t.Fatalf("updated membership = %+v", updated)
	}
	if _, _, err := store.UpdateMembership(adminMutationContext(fixture), first.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipActive)}); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale membership update error = %v, want ErrAuthorizationStateChanged", err)
	}

	page, err := store.ListOrganizationMemberships(fixture.ctx, first.ID, MembershipFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("first organization memberships = %+v, want owner and member only", page.Items)
	}
}

func TestAdminStoreTransferOwnershipIncrementsOrganizationAndMembershipRevisions(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Transfer", Slug: "transfer", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	membership, organization, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: fixture.otherID, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := store.TransferOwnership(adminMutationContext(fixture), created.ID, organization.PolicyRevision, fixture.otherID)
	if err != nil {
		t.Fatal(err)
	}
	if transferred.OwnerAccountID == nil || *transferred.OwnerAccountID != fixture.otherID || transferred.PolicyRevision != organization.PolicyRevision+1 {
		t.Fatalf("transferred organization = %+v", transferred)
	}
	newOwnerMembership, err := store.GetMembership(fixture.ctx, fixture.otherID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newOwnerMembership.LegacyRole != "admin" || newOwnerMembership.Status != MembershipActive || newOwnerMembership.SecurityRevision != membership.SecurityRevision+1 {
		t.Fatalf("new owner membership = %+v", newOwnerMembership)
	}
	if _, err := store.TransferOwnership(adminMutationContext(fixture), created.ID, organization.PolicyRevision, fixture.adminID); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale transfer error = %v, want ErrAuthorizationStateChanged", err)
	}
}

func TestAdminStoreAuditFailureRollsBackOrganizationMutation(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Atomic", Slug: "atomic", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		CREATE FUNCTION reject_test_admin_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced audit failure'; END $$;
		CREATE TRIGGER reject_test_admin_audit BEFORE INSERT ON admin_audit_events
		FOR EACH ROW EXECUTE FUNCTION reject_test_admin_audit()`); err != nil {
		t.Fatalf("install failing audit trigger: %v", err)
	}

	name := "Must Roll Back"
	if _, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name}); err == nil {
		t.Fatal("UpdateOrganization succeeded despite forced audit failure")
	}
	current, err := store.GetOrganization(fixture.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != created.Name || current.PolicyRevision != created.PolicyRevision {
		t.Fatalf("organization changed after audit failure: before %+v after %+v", created, current)
	}
}

func TestAdminStoreAuditCapturesLockedBeforeAfterStateAndStaleAttemptDoesNotAudit(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Before", Slug: "accurate", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	name := "After"
	updated, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name}); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale update error = %v", err)
	}
	var beforeRevision, afterRevision int64
	var beforeName, afterName string
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT before_revision, after_revision, before_state->>'name', after_state->>'name'
		FROM admin_audit_events
		WHERE organization_id = $1 AND action = 'organization.renamed'`, created.ID).Scan(&beforeRevision, &afterRevision, &beforeName, &afterName); err != nil {
		t.Fatalf("load rename audit: %v", err)
	}
	if beforeRevision != created.PolicyRevision || afterRevision != updated.PolicyRevision || beforeName != "Before" || afterName != "After" {
		t.Fatalf("audit = revisions %d/%d names %q/%q", beforeRevision, afterRevision, beforeName, afterName)
	}
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM admin_audit_events WHERE organization_id=$1 AND action='organization.renamed'`, created.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rename audit count = %d, err = %v", count, err)
	}
}

func TestAdminStoreUnchangedOrganizationUpdateIsNoOpWithoutAudit(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Unchanged", Slug: "unchanged", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	name, slug := created.Name, created.Slug
	returned, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name, Slug: &slug})
	if err != nil {
		t.Fatal(err)
	}
	if returned.ID != created.ID || returned.Name != created.Name || returned.Slug != created.Slug || returned.Status != created.Status ||
		returned.PolicyRevision != created.PolicyRevision || returned.OwnerAccountID == nil || created.OwnerAccountID == nil || *returned.OwnerAccountID != *created.OwnerAccountID {
		t.Fatalf("unchanged update = %+v, want %+v", returned, created)
	}
	current, err := store.GetOrganization(fixture.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.PolicyRevision != created.PolicyRevision {
		t.Fatalf("policy revision = %d, want unchanged %d", current.PolicyRevision, created.PolicyRevision)
	}
	var auditCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM admin_audit_events
		WHERE organization_id=$1 AND action IN ('organization.renamed', 'organization.slug_changed')`, created.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("unchanged update audit count = %d, want 0", auditCount)
	}

	if _, err := store.UpdateOrganization(adminMutationContext(fixture), created.ID, created.PolicyRevision+1, UpdateOrganizationInput{Name: &name}); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale unchanged update error = %v, want ErrAuthorizationStateChanged", err)
	}
}

func TestAdminStoreTransferOwnershipRejectsDisabledTarget(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Disabled", Slug: "disabled", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	_, organization, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: fixture.otherID, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET enabled=false WHERE id=$1`, fixture.otherID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransferOwnership(adminMutationContext(fixture), created.ID, organization.PolicyRevision, fixture.otherID); !errors.Is(err, ErrOwnerNotEligible) {
		t.Fatalf("disabled owner transfer error = %v, want ErrOwnerNotEligible", err)
	}
}

func TestAdminStoreCreateMembershipDistinguishesMissingAccount(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Missing", Slug: "missing", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: 2147483647, LegacyRole: "user"}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account error = %v, want ErrAccountNotFound", err)
	}
}

func adminMutationContext(fixture tenancyFixture) context.Context {
	return WithAdminMutationActor(fixture.ctx, AdminMutationActor{
		AccountID: fixture.adminID, PlatformRole: "platform_admin", AuthorityContext: "platform", RequestID: "store-test-request",
	})
}

func membershipStatusPtr(status MembershipStatus) *MembershipStatus { return &status }

func TestAdminStoreRejectsMalformedCursor(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	if _, err := store.ListOrganizations(fixture.ctx, OrganizationFilter{Limit: 10, Cursor: "not-a-cursor"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor error = %v, want ErrInvalidCursor", err)
	}
	if _, err := store.GetOrganization(fixture.ctx, uuid.New()); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("missing organization error = %v", err)
	}
}
