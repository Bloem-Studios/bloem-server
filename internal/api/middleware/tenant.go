package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// CodeTenantUnavailable is the tenant gate's refusal when the membership
// lookup could not answer: the error code of Bloem's body and, on /api/v2,
// the X-Bloem-Denial value. ReasonTenantUnavailable is the denial reason the
// v2 gate chain translates.
const (
	CodeTenantUnavailable   = "tenant_unavailable"
	ReasonTenantUnavailable = "tenant_unavailable"
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

// ResolveNative projects an authenticated native request into the account's
// default organization, as ResolveLegacy does. A stream-token request is
// already authorized for its one file and passes through.
func (m *TenantMiddleware) ResolveNative(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsStreamTokenAuthorized(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		m.ResolveLegacy(next).ServeHTTP(w, r)
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
		// The v2 gate chain answers this as Silo's retryable
		// dependency_unavailable (apiv2 bloemDenialProblem); the body here
		// is unchanged for Bloem's own routes.
		recordDenialReason(w, ReasonTenantUnavailable)
		writeTenantError(w, http.StatusServiceUnavailable, CodeTenantUnavailable, "Tenant authorization is unavailable")
	}
}

func writeTenantError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: code, Message: message})
}
