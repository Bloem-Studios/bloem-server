package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type resolverStore interface {
	// OrganizationMembership is Store.OrganizationMembership.
	OrganizationMembership(ctx context.Context, accountID int, organizationID *uuid.UUID) (Organization, Membership, error)
}

// Resolver resolves an account's current membership into request tenant context.
type Resolver struct {
	store resolverStore
}

// NewResolver constructs a tenant resolver backed by store.
func NewResolver(store resolverStore) *Resolver {
	return &Resolver{store: store}
}

// Resolve returns the account's active tenant context. A default organization is
// considered only for legacy requests that do not specify an organization.
func (r *Resolver) Resolve(ctx context.Context, accountID int, requestedOrganizationID *uuid.UUID, legacy bool) (Context, error) {
	if r == nil || r.store == nil {
		return Context{}, ErrTenantUnavailable
	}
	if requestedOrganizationID == nil && !legacy {
		return Context{}, ErrOwnershipResolutionRequired
	}
	organization, membership, member, err := r.read(ctx, accountID, requestedOrganizationID)
	if err != nil {
		return Context{}, err
	}
	return resolveTenant(accountID, organization, membership, member, legacy)
}

// ResolveProfile validates a profile's authoritative organization. Profiles
// in the deployment default organization retain the v1 initializing-tenant
// compatibility exception; a non-default organization never inherits it.
func (r *Resolver) ResolveProfile(ctx context.Context, accountID int, organizationID uuid.UUID) (Context, error) {
	if r == nil || r.store == nil || organizationID == uuid.Nil {
		return Context{}, ErrTenantUnavailable
	}
	organization, membership, member, err := r.read(ctx, accountID, &organizationID)
	if err != nil {
		return Context{}, err
	}
	return resolveTenant(accountID, organization, membership, member, organization.Default)
}

// read loads the organization (the deployment default when organizationID is
// nil) and accountID's membership in it in one statement. member is false when
// the account has no membership there; resolveTenant reports that only after
// judging the organization.
func (r *Resolver) read(ctx context.Context, accountID int, organizationID *uuid.UUID) (organization Organization, membership Membership, member bool, err error) {
	organization, membership, err = r.store.OrganizationMembership(ctx, accountID, organizationID)
	switch {
	case err == nil:
		return organization, membership, true, nil
	case errors.Is(err, ErrMembershipNotFound):
		return organization, Membership{}, false, nil
	case organizationID == nil && errors.Is(err, ErrOwnershipResolutionRequired):
		return Organization{}, Membership{}, false, ErrOwnershipResolutionRequired
	case organizationID == nil:
		return Organization{}, Membership{}, false, fmt.Errorf("%w: load default organization: %w", ErrTenantUnavailable, err)
	case errors.Is(err, ErrOrganizationNotFound):
		return Organization{}, Membership{}, false, ErrTenantNotFoundOrHidden
	default:
		return Organization{}, Membership{}, false, fmt.Errorf("%w: load organization: %w", ErrTenantUnavailable, err)
	}
}

func resolveTenant(accountID int, organization Organization, membership Membership, member, legacy bool) (Context, error) {
	if organization.Status == OrganizationSuspended {
		return Context{}, ErrTenantSuspended
	}
	if !legacy && (organization.Status != OrganizationActive || organization.OwnerAccountID == nil) {
		return Context{}, ErrOwnershipResolutionRequired
	}
	if !member {
		return Context{}, ErrTenantNotFoundOrHidden
	}
	if membership.OrganizationID != organization.ID || membership.AccountID != accountID || membership.Status == MembershipInvited {
		return Context{}, ErrTenantNotFoundOrHidden
	}
	if membership.Status == MembershipSuspended {
		return Context{}, ErrTenantSuspended
	}
	if membership.Status != MembershipActive {
		return Context{}, ErrTenantUnavailable
	}

	return Context{
		OrganizationID:      organization.ID,
		MembershipID:        membership.ID,
		AccountID:           accountID,
		OrganizationStatus:  organization.Status,
		OrganizationDefault: organization.Default,
		MembershipStatus:    membership.Status,
		PolicyRevision:      organization.PolicyRevision,
		SecurityRevision:    membership.SecurityRevision,
		Legacy:              legacy,
	}, nil
}
