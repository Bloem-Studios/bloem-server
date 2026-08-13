package tenancy

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestContextRoundTrip(t *testing.T) {
	want := Context{
		OrganizationID:      uuid.New(),
		MembershipID:        uuid.New(),
		AccountID:           41,
		OrganizationStatus:  OrganizationActive,
		MembershipStatus:    MembershipActive,
		PolicyRevision:      7,
		SecurityRevision:    11,
		Legacy:              true,
		OrganizationDefault: true,
	}

	got, ok := FromContext(WithContext(context.Background(), want))
	if !ok {
		t.Fatal("FromContext returned ok = false")
	}
	if got != want {
		t.Fatalf("FromContext = %#v, want %#v", got, want)
	}
}

func TestContextAbsent(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext returned a tenant context that was not set")
	}
}

func TestResolverUsesExplicitOrganization(t *testing.T) {
	organizationID := uuid.New()
	membershipID := uuid.New()
	resolver := NewResolver(resolverTestStore{
		defaultOrganization: activeOrganization(),
		organizations: map[uuid.UUID]Organization{
			organizationID: activeOrganizationWithID(organizationID),
		},
		memberships: map[resolverMembershipKey]Membership{
			{accountID: 17, organizationID: organizationID}: activeMembership(membershipID, organizationID, 17),
		},
	})

	got, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.OrganizationID != organizationID || got.MembershipID != membershipID || got.AccountID != 17 || got.Legacy {
		t.Fatalf("Resolve = %#v, want explicit organization context", got)
	}
}

func TestResolverUsesDefaultOrganizationOnlyForLegacy(t *testing.T) {
	organizationID := uuid.New()
	membershipID := uuid.New()
	organization := activeOrganizationWithID(organizationID)
	organization.Status = OrganizationInitializing
	organization.Default = true
	resolver := NewResolver(resolverTestStore{
		defaultOrganization: organization,
		organizations:       map[uuid.UUID]Organization{organizationID: organization},
		memberships: map[resolverMembershipKey]Membership{
			{accountID: 17, organizationID: organizationID}: activeMembership(membershipID, organizationID, 17),
		},
	})

	got, err := resolver.Resolve(context.Background(), 17, nil, true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.OrganizationID != organizationID || got.MembershipID != membershipID || !got.Legacy || !got.OrganizationDefault || got.OrganizationStatus != OrganizationInitializing {
		t.Fatalf("Resolve = %#v, want legacy default organization context", got)
	}
}

func TestResolverDoesNotMarkNonDefaultOrganizationAsDefault(t *testing.T) {
	organizationID := uuid.New()
	membershipID := uuid.New()
	organization := activeOrganizationWithID(organizationID)
	resolver := NewResolver(resolverTestStore{
		organizations: map[uuid.UUID]Organization{organizationID: organization},
		memberships: map[resolverMembershipKey]Membership{
			{accountID: 17, organizationID: organizationID}: activeMembership(membershipID, organizationID, 17),
		},
	})

	got, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.OrganizationDefault {
		t.Fatalf("OrganizationDefault = true for non-default organization: %#v", got)
	}
}

func TestResolverRejectsUnavailableV2TenantSelection(t *testing.T) {
	resolver := NewResolver(resolverTestStore{defaultOrganization: activeOrganization()})

	_, err := resolver.Resolve(context.Background(), 17, nil, false)
	if !errors.Is(err, ErrOwnershipResolutionRequired) {
		t.Fatalf("Resolve error = %v, want ErrOwnershipResolutionRequired", err)
	}
}

func TestResolverRejectsActiveOrganizationWithoutOwner(t *testing.T) {
	organizationID := uuid.New()
	membershipID := uuid.New()
	organization := activeOrganizationWithID(organizationID)
	organization.OwnerAccountID = nil
	resolver := NewResolver(resolverTestStore{
		organizations: map[uuid.UUID]Organization{organizationID: organization},
		memberships: map[resolverMembershipKey]Membership{
			{accountID: 17, organizationID: organizationID}: activeMembership(membershipID, organizationID, 17),
		},
	})

	_, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
	if !errors.Is(err, ErrOwnershipResolutionRequired) {
		t.Fatalf("Resolve error = %v, want ErrOwnershipResolutionRequired", err)
	}
}

func TestResolverHidesUnknownOrganization(t *testing.T) {
	organizationID := uuid.New()
	resolver := NewResolver(resolverTestStore{})

	_, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
	if !errors.Is(err, ErrTenantNotFoundOrHidden) {
		t.Fatalf("Resolve error = %v, want ErrTenantNotFoundOrHidden", err)
	}
}

func TestResolverRejectsUnavailableStore(t *testing.T) {
	var resolver Resolver
	organizationID := uuid.New()

	_, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
	if !errors.Is(err, ErrTenantUnavailable) {
		t.Fatalf("Resolve error = %v, want ErrTenantUnavailable", err)
	}
}

func TestResolverRejectsUnavailableOrUnsafeTenantState(t *testing.T) {
	organizationID := uuid.New()
	activeOrganization := activeOrganizationWithID(organizationID)
	activeMembership := activeMembership(uuid.New(), organizationID, 17)

	tests := []struct {
		name  string
		store resolverTestStore
		want  error
	}{
		{
			name: "absent membership is hidden",
			store: resolverTestStore{
				organizations: map[uuid.UUID]Organization{organizationID: activeOrganization},
			},
			want: ErrTenantNotFoundOrHidden,
		},
		{
			name: "suspended membership",
			store: resolverTestStore{
				organizations: map[uuid.UUID]Organization{organizationID: activeOrganization},
				memberships: map[resolverMembershipKey]Membership{
					{accountID: 17, organizationID: organizationID}: func() Membership {
						membership := activeMembership
						membership.Status = MembershipSuspended
						return membership
					}(),
				},
			},
			want: ErrTenantSuspended,
		},
		{
			name: "suspended organization",
			store: resolverTestStore{
				organizations: map[uuid.UUID]Organization{
					organizationID: func() Organization {
						organization := activeOrganization
						organization.Status = OrganizationSuspended
						return organization
					}(),
				},
				memberships: map[resolverMembershipKey]Membership{
					{accountID: 17, organizationID: organizationID}: activeMembership,
				},
			},
			want: ErrTenantSuspended,
		},
		{
			name: "initializing organization requires ownership resolution for v2",
			store: resolverTestStore{
				organizations: map[uuid.UUID]Organization{
					organizationID: func() Organization {
						organization := activeOrganization
						organization.Status = OrganizationInitializing
						return organization
					}(),
				},
				memberships: map[resolverMembershipKey]Membership{
					{accountID: 17, organizationID: organizationID}: activeMembership,
				},
			},
			want: ErrOwnershipResolutionRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewResolver(tt.store)
			_, err := resolver.Resolve(context.Background(), 17, &organizationID, false)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Resolve error = %v, want %v", err, tt.want)
			}
		})
	}
}

type resolverMembershipKey struct {
	accountID      int
	organizationID uuid.UUID
}

type resolverTestStore struct {
	defaultOrganization Organization
	defaultErr          error
	organizations       map[uuid.UUID]Organization
	memberships         map[resolverMembershipKey]Membership
}

func (s resolverTestStore) DefaultOrganization(context.Context) (Organization, error) {
	if s.defaultErr != nil {
		return Organization{}, s.defaultErr
	}
	return s.defaultOrganization, nil
}

func (s resolverTestStore) GetOrganization(_ context.Context, organizationID uuid.UUID) (Organization, error) {
	organization, ok := s.organizations[organizationID]
	if !ok {
		return Organization{}, ErrOrganizationNotFound
	}
	return organization, nil
}

func (s resolverTestStore) GetMembership(_ context.Context, accountID int, organizationID uuid.UUID) (Membership, error) {
	membership, ok := s.memberships[resolverMembershipKey{accountID: accountID, organizationID: organizationID}]
	if !ok {
		return Membership{}, ErrMembershipNotFound
	}
	return membership, nil
}

func activeOrganization() Organization {
	return activeOrganizationWithID(uuid.New())
}

func activeOrganizationWithID(organizationID uuid.UUID) Organization {
	ownerAccountID := 1
	return Organization{ID: organizationID, Status: OrganizationActive, OwnerAccountID: &ownerAccountID, PolicyRevision: 5}
}

func activeMembership(membershipID, organizationID uuid.UUID, accountID int) Membership {
	return Membership{
		ID:               membershipID,
		OrganizationID:   organizationID,
		AccountID:        accountID,
		Status:           MembershipActive,
		SecurityRevision: 9,
	}
}
