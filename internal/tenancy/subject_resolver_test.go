package tenancy

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type subjectProfileOrganizationStub struct {
	organizationID uuid.UUID
	err            error
	accountID      int
	profileID      string
	// accountOrganizationID is what a profile-less subject resolves through.
	// uuid.Nil with no error keeps the legacy default-organization fallback.
	accountOrganizationID  uuid.UUID
	accountOrganizationErr error
}

func (s *subjectProfileOrganizationStub) AccountOrganization(_ context.Context, accountID int) (uuid.UUID, error) {
	s.accountID = accountID
	if s.accountOrganizationErr != nil {
		return uuid.Nil, s.accountOrganizationErr
	}
	if s.accountOrganizationID == uuid.Nil {
		return uuid.Nil, ErrMembershipNotFound
	}
	return s.accountOrganizationID, nil
}

type subjectResolverStoreStub struct {
	organization Organization
	membership   Membership
}

func (s subjectResolverStoreStub) DefaultOrganization(context.Context) (Organization, error) {
	return s.organization, nil
}

func (s subjectResolverStoreStub) GetOrganization(context.Context, uuid.UUID) (Organization, error) {
	return s.organization, nil
}

func (s subjectResolverStoreStub) GetMembership(context.Context, int, uuid.UUID) (Membership, error) {
	return s.membership, nil
}

func (s *subjectProfileOrganizationStub) ProfileOrganization(_ context.Context, accountID int, profileID string) (uuid.UUID, error) {
	s.accountID, s.profileID = accountID, profileID
	return s.organizationID, s.err
}

func TestSubjectResolverUsesProfileOrganizationAndValidatesMembership(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	ownerID := 17
	store := subjectResolverStoreStub{
		organization: Organization{ID: organizationID, Status: OrganizationActive, OwnerAccountID: &ownerID, PolicyRevision: 4},
		membership:   Membership{ID: membershipID, OrganizationID: organizationID, AccountID: 17, Status: MembershipActive, SecurityRevision: 5},
	}
	profiles := &subjectProfileOrganizationStub{organizationID: organizationID}
	resolver := NewSubjectResolver(NewResolver(store), profiles)

	got, err := resolver.ResolveSubjectTenant(context.Background(), 17, "profile-17")
	if err != nil {
		t.Fatalf("ResolveSubjectTenant() error: %v", err)
	}
	if got.OrganizationID != organizationID || got.MembershipID != membershipID || got.AccountID != 17 || got.Legacy {
		t.Fatalf("tenant = %#v, want validated profile organization", got)
	}
	if profiles.accountID != 17 || profiles.profileID != "profile-17" {
		t.Fatalf("profile lookup = %d/%q", profiles.accountID, profiles.profileID)
	}
}

func TestSubjectResolverRejectsMissingOrForeignProfile(t *testing.T) {
	wantErr := errors.New("profile hidden")
	profiles := &subjectProfileOrganizationStub{err: wantErr}
	resolver := NewSubjectResolver(NewResolver(subjectResolverStoreStub{}), profiles)
	if _, err := resolver.ResolveSubjectTenant(context.Background(), 17, "foreign"); !errors.Is(err, wantErr) {
		t.Fatalf("ResolveSubjectTenant() error = %v, want %v", err, wantErr)
	}
}

func TestSubjectResolverPreservesOnlyAuthoritativeDefaultProfileCompatibility(t *testing.T) {
	organizationID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	membershipID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	store := subjectResolverStoreStub{
		organization: Organization{
			ID:             organizationID,
			Status:         OrganizationInitializing,
			Default:        true,
			PolicyRevision: 4,
		},
		membership: Membership{
			ID:               membershipID,
			OrganizationID:   organizationID,
			AccountID:        17,
			Status:           MembershipActive,
			SecurityRevision: 5,
		},
	}
	profiles := &subjectProfileOrganizationStub{organizationID: organizationID}
	resolver := NewSubjectResolver(NewResolver(store), profiles)

	got, err := resolver.ResolveSubjectTenant(context.Background(), 17, "legacy-profile")
	if err != nil {
		t.Fatalf("ResolveSubjectTenant(default profile) error: %v", err)
	}
	if !got.Legacy || !got.OrganizationDefault || got.OrganizationID != organizationID {
		t.Fatalf("default profile tenant = %#v, want authoritative legacy default provenance", got)
	}

	store.organization.Default = false
	resolver = NewSubjectResolver(NewResolver(store), profiles)
	if _, err := resolver.ResolveSubjectTenant(context.Background(), 17, "foreign-initializing"); !errors.Is(err, ErrOwnershipResolutionRequired) {
		t.Fatalf("ResolveSubjectTenant(non-default initializing) error = %v, want ownership required", err)
	}
}
