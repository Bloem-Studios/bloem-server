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
	// An account belongs to one organization, so the owner is a new account.
	owner := fixture.insertAccount(t, "owner", "user")
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{
		Name: "North Sea Media", Slug: "north-sea-media", OwnerAccountID: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != OrganizationActive || created.PolicyRevision != 1 || created.OwnerAccountID == nil || *created.OwnerAccountID != owner {
		t.Fatalf("organization = %+v", created)
	}
	membership, err := store.GetMembership(fixture.ctx, owner, created.ID)
	if err != nil || membership.LegacyRole != "admin" || membership.Status != MembershipActive {
		t.Fatalf("membership = %+v, err = %v", membership, err)
	}
	bundleMembers := creationDefaultBundleMembers(t, fixture)
	summary, err := store.GetOrganizationSummary(fixture.ctx, created.ID)
	if err != nil || summary.MembershipCount != 1 || summary.ActiveMembershipCount != 1 || summary.ProfileCount != 0 || summary.LibraryCount != 0 || summary.EntitlementCount != bundleMembers {
		t.Fatalf("organization summary = %+v, err = %v", summary, err)
	}

	_, err = store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{
		Name: "Duplicate", Slug: "NORTH-SEA-MEDIA", OwnerAccountID: fixture.insertAccount(t, "owner-1", "user"),
	})
	if !errors.Is(err, ErrOrganizationSlugConflict) {
		t.Fatalf("duplicate slug error = %v, want ErrOrganizationSlugConflict", err)
	}
}

func TestAdminStoreListOrganizationsUsesStableCursorAndExactCounts(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	for _, input := range []CreateOrganizationInput{
		{Name: "alpha", Slug: "alpha", OwnerAccountID: fixture.insertAccount(t, "owner-2", "user")},
		{Name: "Alpha", Slug: "alpha-two", OwnerAccountID: fixture.insertAccount(t, "owner-3", "user")},
		{Name: "Zulu", Slug: "zulu", OwnerAccountID: fixture.insertAccount(t, "owner-4", "user")},
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
	bundleMembers := creationDefaultBundleMembers(t, fixture)
	for _, item := range append(first.Items, second.Items...) {
		if item.MembershipCount != 1 || item.ActiveMembershipCount != 1 || item.ProfileCount != 0 || item.LibraryCount != 0 || item.EntitlementCount != bundleMembers {
			t.Fatalf("counts for %s = memberships %d profiles %d libraries %d entitlements %d", item.ID, item.MembershipCount, item.ProfileCount, item.LibraryCount, item.EntitlementCount)
		}
	}
}

func TestAdminStoreUpdateAndSuspensionAreRevisionGuardedAndReversible(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "North Sea", Slug: "north-sea", OwnerAccountID: fixture.insertAccount(t, "owner-5", "user")})
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

func TestAdminStoreOrganizationUpdateCallerOwnedTransactionRollsBack(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Before", Slug: "rollback-update", OwnerAccountID: fixture.insertAccount(t, "owner-6", "user")})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "After"
	if _, err := store.UpdateOrganizationInTransaction(adminMutationContext(fixture), tx, created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(fixture.ctx); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetOrganization(fixture.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name != created.Name || current.PolicyRevision != created.PolicyRevision {
		t.Fatalf("organization update survived rollback: before %+v after %+v", created, current)
	}
}

func TestAdminStoreMembershipLifecycleIsOrganizationBounded(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	first, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "First", Slug: "first", OwnerAccountID: fixture.insertAccount(t, "owner-7", "user")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Second", Slug: "second", OwnerAccountID: fixture.insertAccount(t, "owner-8", "user")})
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
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Transfer", Slug: "transfer", OwnerAccountID: fixture.insertAccount(t, "owner-9", "user")})
	if err != nil {
		t.Fatal(err)
	}
	next := fixture.insertAccount(t, "next-owner", "user")
	membership, organization, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: next, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := store.TransferOwnership(adminMutationContext(fixture), created.ID, organization.PolicyRevision, next)
	if err != nil {
		t.Fatal(err)
	}
	if transferred.OwnerAccountID == nil || *transferred.OwnerAccountID != next || transferred.PolicyRevision != organization.PolicyRevision+1 {
		t.Fatalf("transferred organization = %+v", transferred)
	}
	newOwnerMembership, err := store.GetMembership(fixture.ctx, next, created.ID)
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
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Atomic", Slug: "atomic", OwnerAccountID: fixture.insertAccount(t, "owner-10", "user")})
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
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Before", Slug: "accurate", OwnerAccountID: fixture.insertAccount(t, "owner-11", "user")})
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
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Unchanged", Slug: "unchanged", OwnerAccountID: fixture.insertAccount(t, "owner-12", "user")})
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
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Disabled", Slug: "disabled", OwnerAccountID: fixture.insertAccount(t, "owner-13", "user")})
	if err != nil {
		t.Fatal(err)
	}
	member := fixture.insertAccount(t, "disabled-member", "user")
	_, organization, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: member, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE users SET enabled=false WHERE id=$1`, member); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TransferOwnership(adminMutationContext(fixture), created.ID, organization.PolicyRevision, member); !errors.Is(err, ErrOwnerNotEligible) {
		t.Fatalf("disabled owner transfer error = %v, want ErrOwnerNotEligible", err)
	}
}

func TestAdminStoreCreateMembershipDistinguishesMissingAccount(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(adminMutationContext(fixture), CreateOrganizationInput{Name: "Missing", Slug: "missing", OwnerAccountID: fixture.insertAccount(t, "owner-14", "user")})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateMembership(adminMutationContext(fixture), created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: 2147483647, LegacyRole: "user"}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account error = %v, want ErrAccountNotFound", err)
	}
}

// creationDefaultBundleMembers counts the entitlements a new organization
// receives from the creation-default bundle.
func creationDefaultBundleMembers(t *testing.T, fixture tenancyFixture) int64 {
	t.Helper()
	var members int64
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM entitlement_bundle_members AS members
		JOIN entitlement_bundles AS bundles
		  ON bundles.id = members.bundle_id AND bundles.active_revision = members.bundle_revision
		WHERE bundles.is_organization_creation_default AND bundles.status = 'active'`).Scan(&members); err != nil {
		t.Fatalf("count creation-default bundle members: %v", err)
	}
	return members
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
