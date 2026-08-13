package tenancy_test

import (
	"errors"
	"testing"

	. "github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

func TestAdminStoreCreateOrganizationCreatesOwnerMembership(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	created, err := store.CreateOrganization(fixture.ctx, CreateOrganizationInput{
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

	_, err = store.CreateOrganization(fixture.ctx, CreateOrganizationInput{
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
		if _, err := store.CreateOrganization(fixture.ctx, input); err != nil {
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
	created, err := store.CreateOrganization(fixture.ctx, CreateOrganizationInput{Name: "North Sea", Slug: "north-sea", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	name := "North Sea Media"
	updated, err := store.UpdateOrganization(fixture.ctx, created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.PolicyRevision != created.PolicyRevision+1 {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := store.UpdateOrganization(fixture.ctx, created.ID, created.PolicyRevision, UpdateOrganizationInput{Name: &name}); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale update error = %v, want ErrAuthorizationStateChanged", err)
	}
	suspended, err := store.SetOrganizationStatus(fixture.ctx, created.ID, updated.PolicyRevision, OrganizationSuspended)
	if err != nil {
		t.Fatal(err)
	}
	reactivated, err := store.SetOrganizationStatus(fixture.ctx, created.ID, suspended.PolicyRevision, OrganizationActive)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.Status != OrganizationSuspended || reactivated.Status != OrganizationActive || reactivated.PolicyRevision != suspended.PolicyRevision+1 {
		t.Fatalf("suspended/reactivated = %+v / %+v", suspended, reactivated)
	}
}

func TestAdminStoreMembershipLifecycleIsOrganizationBounded(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	first, err := store.CreateOrganization(fixture.ctx, CreateOrganizationInput{Name: "First", Slug: "first", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateOrganization(fixture.ctx, CreateOrganizationInput{Name: "Second", Slug: "second", OwnerAccountID: fixture.otherID})
	if err != nil {
		t.Fatal(err)
	}
	memberAccountID := fixture.insertAccount(t, "member", "user")
	membership, organization, err := store.CreateMembership(fixture.ctx, first.ID, first.PolicyRevision, CreateMembershipInput{AccountID: memberAccountID, LegacyRole: "user"})
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

	if _, _, err := store.UpdateMembership(fixture.ctx, second.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipSuspended)}); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("cross-organization update error = %v, want ErrMembershipNotFound", err)
	}
	updated, _, err := store.UpdateMembership(fixture.ctx, first.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipSuspended)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != MembershipSuspended || updated.SecurityRevision != membership.SecurityRevision+1 {
		t.Fatalf("updated membership = %+v", updated)
	}
	if _, _, err := store.UpdateMembership(fixture.ctx, first.ID, membership.ID, membership.SecurityRevision, UpdateMembershipInput{Status: membershipStatusPtr(MembershipActive)}); !errors.Is(err, ErrAuthorizationStateChanged) {
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
	created, err := store.CreateOrganization(fixture.ctx, CreateOrganizationInput{Name: "Transfer", Slug: "transfer", OwnerAccountID: fixture.adminID})
	if err != nil {
		t.Fatal(err)
	}
	membership, organization, err := store.CreateMembership(fixture.ctx, created.ID, created.PolicyRevision, CreateMembershipInput{AccountID: fixture.otherID, LegacyRole: "user"})
	if err != nil {
		t.Fatal(err)
	}
	transferred, err := store.TransferOwnership(fixture.ctx, created.ID, organization.PolicyRevision, fixture.otherID)
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
	if _, err := store.TransferOwnership(fixture.ctx, created.ID, organization.PolicyRevision, fixture.adminID); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale transfer error = %v, want ErrAuthorizationStateChanged", err)
	}
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
