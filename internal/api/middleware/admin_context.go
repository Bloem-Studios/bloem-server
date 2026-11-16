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

// SupportAccessValidator confirms a break-glass support access is still open for
// the operator, login session and organization a pass names, and returns the
// organization's current policy revision. Any error other than a transient one
// must satisfy errors.Is(err, ErrSupportAccessInvalid).
type SupportAccessValidator interface {
	ValidateSupportAccess(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID string) (int64, error)
}

// ErrSupportAccessInvalid marks an access that is closed, expired, foreign, or
// whose operator or organization no longer qualifies.
var ErrSupportAccessInvalid = errors.New("support access is not valid")

// AdminContextMiddleware validates an administrative context token and then
// revalidates the authority it encodes on every /api/bloem/v2/admin request.
type AdminContextMiddleware struct {
	tokens      auth.AdminContextTokenService
	resolver    TenantResolver
	memberships AdminContextMembershipStore
	platform    auth.PlatformAdminAuthorizer
	// sessions revalidates the login session each context was exchanged
	// from. Nil denies every request: an unwired validator must not mean a
	// context survives the revocation of the session behind it.
	sessions SessionValidator
	// supportAccess revalidates a pass opened under break-glass support
	// access. Nil denies every such pass.
	supportAccess SupportAccessValidator
}

// SetSupportAccess installs the validator for passes opened under support access.
func (m *AdminContextMiddleware) SetSupportAccess(validator SupportAccessValidator) {
	if m != nil {
		m.supportAccess = validator
	}
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
		ctx, refusal := m.Authorize(r.Context(), claims)
		if refusal != nil {
			refusal.Write(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminContextRefusal is why an administrative context does not hold: the HTTP
// answer the routes give, and the reason a long-lived connection that was
// opened under the context closes.
type AdminContextRefusal struct {
	Status  int
	Code    string
	Message string
}

// Transient reports a refusal that says the check could not be made, not that
// the context stopped holding. A connection keeps going through one.
func (r *AdminContextRefusal) Transient() bool { return r.Status >= http.StatusInternalServerError }

// Write answers the request with the refusal. No cache may keep it.
func (r *AdminContextRefusal) Write(w http.ResponseWriter) {
	writeAdminContextError(w, r.Status, r.Code, r.Message)
}

func refuse(status int, code, message string) *AdminContextRefusal {
	return &AdminContextRefusal{Status: status, Code: code, Message: message}
}

// Authorize revalidates the authority claims encode against the current
// session, account, membership and organization, and returns the context a
// handler runs in. It is everything Require does after parsing the token, so
// a connection that outlives its request (the organization live socket)
// revalidates with exactly the rules the REST routes apply.
func (m *AdminContextMiddleware) Authorize(ctx context.Context, claims auth.AdminContextClaims) (context.Context, *AdminContextRefusal) {
	// A context lives no longer than the login session it was exchanged
	// from: logout or session revocation ends it on the next request.
	if claims.SessionID == "" {
		return ctx, refuse(http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
	}
	if m == nil || m.sessions == nil {
		return ctx, refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
	if authority, valid, err := m.sessions.ActiveSession(ctx, claims.SessionID, claims.AccountID); err != nil {
		return ctx, refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	} else if !valid || authority.IsRemotePlayback() {
		return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	}

	// The account behind the token is re-read on every request, for both
	// scopes: a disabled or replaced account loses administrative access
	// immediately rather than when the token expires.
	operator, err := m.operator(ctx, claims)
	if err != nil {
		return ctx, operatorRefusal(err)
	}

	switch claims.Scope {
	case auth.AdminScopePlatform:
		if !operator.PlatformAdmin {
			return ctx, refuse(http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
		}
		return SetAdminContextClaims(ctx, claims), nil
	case auth.AdminScopeOrganization:
		if claims.SupportAccessID != uuid.Nil {
			return m.authorizeSupportAccess(ctx, claims, operator)
		}
		resolved, err := m.resolve(ctx, claims.AccountID, &claims.OrganizationID, false)
		if err != nil {
			return ctx, tenantResolveRefusal(err)
		}
		if resolved.AccountID != claims.AccountID || resolved.OrganizationID != claims.OrganizationID ||
			resolved.MembershipID != claims.MembershipID || resolved.PolicyRevision != claims.PolicyRevision ||
			resolved.SecurityRevision != claims.SecurityRevision {
			return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		}

		membership, err := m.membership(ctx, claims.AccountID, claims.OrganizationID)
		if err != nil {
			return ctx, membershipRefusal(err)
		}
		if membership.ID != claims.MembershipID || membership.AccountID != claims.AccountID || membership.OrganizationID != claims.OrganizationID || membership.Status != tenancy.MembershipActive {
			return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		}
		if claims.EffectiveAuthority == "platform_admin" {
			if !operator.PlatformAdmin {
				return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
			}
		} else if claims.EffectiveAuthority != "organization_admin" || membership.LegacyRole != "admin" {
			return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		}
		return tenancy.WithContext(SetAdminContextClaims(ctx, claims), resolved), nil
	default:
		return ctx, refuse(http.StatusUnauthorized, "tenant_session_required", "Valid administrative context required")
	}
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

func operatorRefusal(err error) *AdminContextRefusal {
	switch {
	case errors.Is(err, auth.ErrOperatorIneligible):
		return refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	default:
		return refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

func membershipRefusal(err error) *AdminContextRefusal {
	switch {
	case errors.Is(err, tenancy.ErrMembershipNotFound):
		return refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	default:
		return refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

// tenantResolveRefusal is writeTenantResolveError's answer as a value.
func tenantResolveRefusal(err error) *AdminContextRefusal {
	switch {
	case errors.Is(err, tenancy.ErrTenantSuspended):
		return refuse(http.StatusForbidden, "organization_suspended", "Tenant access is suspended")
	case errors.Is(err, tenancy.ErrTenantNotFoundOrHidden), errors.Is(err, tenancy.ErrOwnershipResolutionRequired):
		return refuse(http.StatusUnauthorized, "tenant_session_required", "Valid tenant session required")
	default:
		return refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

// writeAdminContextError is writeTenantError for an administrative refusal,
// which no cache may keep.
func writeAdminContextError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	writeTenantError(w, status, code, message)
}
