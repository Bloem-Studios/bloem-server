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

type adminContextClaimsKey struct{}

// SetAdminContextClaims stores claims only after administrative context token
// validation. It is exported so handlers can consume the validated actor and
// scope without reparsing the bearer token.
func SetAdminContextClaims(ctx context.Context, claims auth.AdminContextClaims) context.Context {
	return context.WithValue(ctx, adminContextClaimsKey{}, claims)
}

// GetAdminContextClaims returns the server-validated administrative context.
func GetAdminContextClaims(ctx context.Context) (auth.AdminContextClaims, bool) {
	claims, ok := ctx.Value(adminContextClaimsKey{}).(auth.AdminContextClaims)
	return claims, ok
}

// AdminContextMiddleware validates an administrative context token and then
// revalidates the authority it encodes on every /api/bloem/v1/admin request.
type AdminContextMiddleware struct {
	tokens      auth.AdminContextTokenService
	resolver    TenantResolver
	memberships AdminContextMembershipStore
	platform    auth.PlatformAdminAuthorizer
	// sessions revalidates the login session each context was exchanged
	// from. Nil denies every request: an unwired validator must not mean a
	// context survives the revocation of the session behind it.
	sessions SessionValidator
}

// LoginSessions returns the validator this middleware checks login sessions
// with, so a credential derived from a login session (an administrative
// context) is revoked by exactly the same check. Nil when unwired.
func (am *AuthMiddleware) LoginSessions() SessionValidator {
	if am == nil {
		return nil
	}
	return am.sessionValidator
}

func NewAdminContextMiddleware(tokens auth.AdminContextTokenService, resolver TenantResolver, memberships AdminContextMembershipStore, platform auth.PlatformAdminAuthorizer, sessions SessionValidator) *AdminContextMiddleware {
	return &AdminContextMiddleware{tokens: tokens, resolver: resolver, memberships: memberships, platform: platform, sessions: sessions}
}

func (m *AdminContextMiddleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := m.parse(r.Header.Get("Authorization"))
		if err != nil {
			writeAdminContextError(w, http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
			return
		}

		// A context lives no longer than the login session it was exchanged
		// from: logout or session revocation ends it on the next request.
		if claims.SessionID == "" {
			writeAdminContextError(w, http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
			return
		}
		if m.sessions == nil {
			writeAdminContextError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
			return
		}
		if authority, valid, err := m.sessions.ActiveSession(r.Context(), claims.SessionID, claims.AccountID); err != nil {
			writeAdminContextError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
			return
		} else if !valid || authority.IsRemotePlayback() {
			writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
			return
		}

		// The account behind the token is re-read on every request, for both
		// scopes: a disabled or replaced account loses administrative access
		// immediately rather than when the token expires.
		operator, err := m.operator(r.Context(), claims)
		if err != nil {
			writeAdminOperatorError(w, err)
			return
		}

		switch claims.Scope {
		case auth.AdminScopePlatform:
			if !operator.PlatformAdmin {
				writeAdminContextError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
				return
			}
			next.ServeHTTP(w, r.WithContext(SetAdminContextClaims(r.Context(), claims)))
		case auth.AdminScopeOrganization:
			resolved, err := m.resolve(r.Context(), claims.AccountID, &claims.OrganizationID, false)
			if err != nil {
				w.Header().Set("Cache-Control", "no-store")
				writeTenantResolveError(w, err)
				return
			}
			if resolved.AccountID != claims.AccountID || resolved.OrganizationID != claims.OrganizationID ||
				resolved.MembershipID != claims.MembershipID || resolved.PolicyRevision != claims.PolicyRevision ||
				resolved.SecurityRevision != claims.SecurityRevision {
				writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
				return
			}

			membership, err := m.membership(r.Context(), claims.AccountID, claims.OrganizationID)
			if err != nil {
				writeAdminContextMembershipError(w, err)
				return
			}
			if membership.ID != claims.MembershipID || membership.AccountID != claims.AccountID || membership.OrganizationID != claims.OrganizationID || membership.Status != tenancy.MembershipActive {
				writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
				return
			}
			if claims.EffectiveAuthority == "platform_admin" {
				if !operator.PlatformAdmin {
					writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
					return
				}
			} else if claims.EffectiveAuthority != "organization_admin" || membership.LegacyRole != "admin" {
				writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
				return
			}
			ctx := SetAdminContextClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(tenancy.WithContext(ctx, resolved)))
		default:
			writeAdminContextError(w, http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
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

// operator re-reads the account named by claims. It fails closed: an
// unwired authorizer, or one without the operator capability, is unavailable.
func (m *AdminContextMiddleware) operator(ctx context.Context, claims auth.AdminContextClaims) (auth.OperatorAuthority, error) {
	if m == nil || m.platform == nil {
		return auth.OperatorAuthority{}, auth.ErrOperatorAuthorityUnavailable
	}
	return auth.ResolveOperatorAuthority(ctx, m.platform, claims.AccountID, claims.AccountIncarnationID)
}

func writeAdminOperatorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrOperatorIneligible):
		writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	default:
		writeAdminContextError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

func writeAdminContextMembershipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenancy.ErrMembershipNotFound):
		writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	default:
		writeAdminContextError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

// writeAdminContextError is writeTenantError for an administrative refusal,
// which no cache may keep.
func writeAdminContextError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeTenantError(w, status, code, message)
}
