package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// AdminContextMembershipStore supplies the current role of a resolved
// organization membership. It prevents a formerly-admin membership from
// continuing to use an already-minted token after a role change.
type AdminContextMembershipStore interface {
	GetMembership(context.Context, int, uuid.UUID) (tenancy.Membership, error)
}

// AdminContextMiddleware validates an administrative context token and then
// revalidates the authority it encodes on every /api/v2/admin request.
type AdminContextMiddleware struct {
	tokens      auth.AdminContextTokenService
	resolver    TenantResolver
	memberships AdminContextMembershipStore
	platform    auth.PlatformAdminAuthorizer
}

func NewAdminContextMiddleware(tokens auth.AdminContextTokenService, resolver TenantResolver, memberships AdminContextMembershipStore, platform auth.PlatformAdminAuthorizer) *AdminContextMiddleware {
	return &AdminContextMiddleware{tokens: tokens, resolver: resolver, memberships: memberships, platform: platform}
}

func (m *AdminContextMiddleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := m.parse(r.Header.Get("Authorization"))
		if err != nil {
			writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
			return
		}

		switch claims.Scope {
		case auth.AdminScopePlatform:
			allowed, err := m.platformAdmin(r.Context(), claims.AccountID)
			if err != nil {
				writeTenantError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
				return
			}
			if !allowed {
				writeTenantError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
				return
			}
			next.ServeHTTP(w, r)
		case auth.AdminScopeOrganization:
			resolved, err := m.resolve(r.Context(), claims.AccountID, &claims.OrganizationID, false)
			if err != nil {
				writeTenantResolveError(w, err)
				return
			}
			if resolved.AccountID != claims.AccountID || resolved.OrganizationID != claims.OrganizationID ||
				resolved.MembershipID != claims.MembershipID || resolved.PolicyRevision != claims.PolicyRevision ||
				resolved.SecurityRevision != claims.SecurityRevision {
				writeTenantError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
				return
			}

			membership, err := m.membership(r.Context(), claims.AccountID, claims.OrganizationID)
			if err != nil {
				writeAdminContextMembershipError(w, err)
				return
			}
			if membership.ID != claims.MembershipID || membership.AccountID != claims.AccountID || membership.OrganizationID != claims.OrganizationID || membership.Status != tenancy.MembershipActive {
				writeTenantError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
				return
			}
			if membership.LegacyRole != "admin" {
				allowed, err := m.platformAdmin(r.Context(), claims.AccountID)
				if err != nil {
					writeTenantError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
					return
				}
				if !allowed {
					writeTenantError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), resolved)))
		default:
			writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
		}
	})
}

func (m *AdminContextMiddleware) parse(header string) (auth.AdminContextClaims, error) {
	if m == nil || m.tokens == nil {
		return auth.AdminContextClaims{}, auth.ErrInvalidAdminContext
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return auth.AdminContextClaims{}, auth.ErrInvalidAdminContext
	}
	return m.tokens.Parse(parts[1])
}

func (m *AdminContextMiddleware) membership(ctx context.Context, accountID int, organizationID uuid.UUID) (tenancy.Membership, error) {
	if m == nil || m.memberships == nil {
		return tenancy.Membership{}, tenancy.ErrTenantUnavailable
	}
	return m.memberships.GetMembership(ctx, accountID, organizationID)
}

func (m *AdminContextMiddleware) resolve(ctx context.Context, accountID int, organizationID *uuid.UUID, legacy bool) (tenancy.Context, error) {
	if m == nil || m.resolver == nil {
		return tenancy.Context{}, tenancy.ErrTenantUnavailable
	}
	return m.resolver.Resolve(ctx, accountID, organizationID, legacy)
}

func (m *AdminContextMiddleware) platformAdmin(ctx context.Context, accountID int) (bool, error) {
	if m == nil || m.platform == nil {
		return false, tenancy.ErrTenantUnavailable
	}
	return m.platform.IsPlatformAdmin(ctx, accountID)
}

func writeAdminContextMembershipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenancy.ErrMembershipNotFound):
		writeTenantError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	default:
		writeTenantError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}
