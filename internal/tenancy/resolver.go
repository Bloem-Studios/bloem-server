package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type resolverStore interface {
	DefaultOrganization(context.Context) (Organization, error)
	GetOrganization(context.Context, uuid.UUID) (Organization, error)
	GetMembership(context.Context, int, uuid.UUID) (Membership, error)
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

	organization, err := r.resolveOrganization(ctx, requestedOrganizationID, legacy)
	if err != nil {
		return Context{}, err
	}
	if organization.Status == OrganizationSuspended {
		return Context{}, ErrTenantSuspended
	}
	if !legacy && (organization.Status != OrganizationActive || organization.OwnerAccountID == nil) {
		return Context{}, ErrOwnershipResolutionRequired
	}

	membership, err := r.store.GetMembership(ctx, accountID, organization.ID)
	if errors.Is(err, ErrMembershipNotFound) {
		return Context{}, ErrTenantNotFoundOrHidden
	}
	if err != nil {
		return Context{}, fmt.Errorf("%w: load membership: %w", ErrTenantUnavailable, err)
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

func (r *Resolver) resolveOrganization(ctx context.Context, requestedOrganizationID *uuid.UUID, legacy bool) (Organization, error) {
	if requestedOrganizationID == nil {
		if !legacy {
			return Organization{}, ErrOwnershipResolutionRequired
		}
		organization, err := r.store.DefaultOrganization(ctx)
		if errors.Is(err, ErrOwnershipResolutionRequired) {
			return Organization{}, ErrOwnershipResolutionRequired
		}
		if err != nil {
			return Organization{}, fmt.Errorf("%w: load default organization: %w", ErrTenantUnavailable, err)
		}
		return organization, nil
	}

	organization, err := r.store.GetOrganization(ctx, *requestedOrganizationID)
	if errors.Is(err, ErrOrganizationNotFound) {
		return Organization{}, ErrTenantNotFoundOrHidden
	}
	if err != nil {
		return Organization{}, fmt.Errorf("%w: load organization: %w", ErrTenantUnavailable, err)
	}
	return organization, nil
}
