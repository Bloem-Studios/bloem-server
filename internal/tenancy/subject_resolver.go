package tenancy

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ProfileOrganizationResolver loads the organization bound to a profile owned
// by an account. Implementations must hide profiles owned by another account.
type ProfileOrganizationResolver interface {
	ProfileOrganization(context.Context, int, string) (uuid.UUID, error)
	// AccountOrganization answers which organization represents the account when
	// no profile names one. See Store.AccountOrganization.
	AccountOrganization(context.Context, int) (uuid.UUID, error)
}

// SubjectResolver turns an out-of-request account/profile identity into the
// same validated tenant context used by authenticated HTTP middleware.
type SubjectResolver struct {
	tenants  *Resolver
	profiles ProfileOrganizationResolver
}

func NewSubjectResolver(tenants *Resolver, profiles ProfileOrganizationResolver) *SubjectResolver {
	return &SubjectResolver{tenants: tenants, profiles: profiles}
}

// ResolveSubjectTenant validates the profile's owning organization and the
// account's current membership. A profile-less legacy subject resolves only
// through the deployment's default organization.
func (r *SubjectResolver) ResolveSubjectTenant(ctx context.Context, accountID int, profileID string) (Context, error) {
	if r == nil || r.tenants == nil || accountID <= 0 {
		return Context{}, ErrTenantUnavailable
	}
	if profileID == "" {
		// A profile-less subject resolves through the organization that
		// represents the account. Falling straight through to the deployment
		// default told a tenant's own end user that its tenant did not exist,
		// because it holds no membership there — and a Silo client browses
		// profile-less until the viewer picks a profile.
		if r.profiles != nil {
			organizationID, err := r.profiles.AccountOrganization(ctx, accountID)
			switch {
			case err == nil && organizationID != uuid.Nil:
				return r.tenants.ResolveProfile(ctx, accountID, organizationID)
			case errors.Is(err, ErrMembershipNotFound):
				// No membership anywhere: keep the legacy answer so a
				// pre-tenancy account still resolves through the default.
			case err != nil:
				return Context{}, err
			}
		}
		return r.tenants.Resolve(ctx, accountID, nil, true)
	}
	if r.profiles == nil {
		return Context{}, ErrTenantUnavailable
	}
	organizationID, err := r.profiles.ProfileOrganization(ctx, accountID, profileID)
	if err != nil {
		return Context{}, err
	}
	if organizationID == uuid.Nil {
		return Context{}, ErrTenantNotFoundOrHidden
	}
	return r.tenants.ResolveProfile(ctx, accountID, organizationID)
}

// ProfileOrganization returns a profile's organization only when both its ID
// and owning account match.
func (s *Store) ProfileOrganization(ctx context.Context, accountID int, profileID string) (uuid.UUID, error) {
	if s == nil || s.pool == nil || accountID <= 0 || profileID == "" {
		return uuid.Nil, ErrTenantNotFoundOrHidden
	}
	var organizationID uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT organization_id
		FROM user_profiles
		WHERE user_id = $1 AND id = $2`, accountID, profileID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrTenantNotFoundOrHidden
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: load profile organization: %w", ErrTenantUnavailable, err)
	}
	return organizationID, nil
}
