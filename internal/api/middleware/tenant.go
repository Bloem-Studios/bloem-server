package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// TenantResolver revalidates an account's current organization membership.
// The narrow interface keeps request middleware independent from storage.
type TenantResolver interface {
	Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)
}

// TenantMiddleware attaches a server-validated tenant identity to authenticated
// requests. It never accepts organization selection from request headers.
type TenantMiddleware struct {
	resolver TenantResolver
}

func NewTenantMiddleware(resolver TenantResolver) *TenantMiddleware {
	return &TenantMiddleware{resolver: resolver}
}

// RequireV2 requires a tenant-bound session selection and validates it against
// current organization, membership, and revocation revisions.
func (m *TenantMiddleware) RequireV2(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil || claims.UserID <= 0 || claims.PolicyRevision <= 0 || claims.SecurityRevision <= 0 {
			writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid tenant session required")
			return
		}

		organizationID, err := uuid.Parse(claims.OrganizationID)
		if err != nil {
			writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid tenant session required")
			return
		}
		membershipID, err := uuid.Parse(claims.MembershipID)
		if err != nil {
			writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid tenant session required")
			return
		}

		resolved, err := m.resolve(r.Context(), claims.UserID, &organizationID, false)
		if err != nil {
			writeTenantResolveError(w, err)
			return
		}
		if resolved.AccountID != claims.UserID || resolved.OrganizationID != organizationID ||
			resolved.MembershipID != membershipID || resolved.PolicyRevision != claims.PolicyRevision ||
			resolved.SecurityRevision != claims.SecurityRevision {
			writeTenantError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
			return
		}

		next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), resolved)))
	})
}

// ResolveLegacy projects an authenticated Silo-compatible request into the
// deployment's default organization. Any organization header or tenant claim
// supplied by the caller is intentionally ignored.
func (m *TenantMiddleware) ResolveLegacy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil || claims.UserID <= 0 {
			writeTenantError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}

		resolved, err := m.resolve(r.Context(), claims.UserID, nil, true)
		if err != nil {
			writeTenantResolveError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(tenancy.WithContext(r.Context(), resolved)))
	})
}

func (m *TenantMiddleware) resolve(ctx context.Context, accountID int, organizationID *uuid.UUID, legacy bool) (tenancy.Context, error) {
	if m == nil || m.resolver == nil {
		return tenancy.Context{}, tenancy.ErrTenantUnavailable
	}
	return m.resolver.Resolve(ctx, accountID, organizationID, legacy)
}

func writeTenantResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenancy.ErrTenantSuspended):
		writeTenantError(w, http.StatusForbidden, "organization_suspended", "Tenant access is suspended")
	case errors.Is(err, tenancy.ErrTenantNotFoundOrHidden), errors.Is(err, tenancy.ErrOwnershipResolutionRequired):
		writeTenantError(w, http.StatusUnauthorized, "tenant_session_required", "Valid tenant session required")
	default:
		writeTenantError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

func writeTenantError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: code, Message: message})
}
