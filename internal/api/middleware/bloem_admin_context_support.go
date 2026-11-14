package middleware

import (
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// serveSupportAccess serves a request whose pass was opened under break-glass
// support access. Its holder is a platform administrator who is not a member
// of the organization, so there is no membership to revalidate. What is
// revalidated instead, on every request: the operator is still an enabled
// platform administrator, and the access is open, unexpired, opened from this
// login session for this organization, with the operator still the server
// Owner and the organization still active and owned. The tenant context it
// builds has no membership; the organization routes already require the claim
// and the context to agree, and both are empty.
func (m *AdminContextMiddleware) serveSupportAccess(w http.ResponseWriter, r *http.Request, next http.Handler, claims auth.AdminContextClaims, operator auth.OperatorAuthority) {
	if !operator.PlatformAdmin || claims.EffectiveAuthority != "platform_admin" || m.supportAccess == nil {
		writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		return
	}
	policyRevision, err := m.supportAccess.ValidateSupportAccess(r.Context(), claims.SupportAccessID, claims.OrganizationID, claims.AccountID, claims.SessionID)
	if errors.Is(err, ErrSupportAccessInvalid) {
		writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		return
	}
	if err != nil {
		writeAdminContextError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	if policyRevision != claims.PolicyRevision {
		writeAdminContextError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
		return
	}
	tenant := tenancy.Context{
		OrganizationID: claims.OrganizationID, AccountID: claims.AccountID,
		OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: policyRevision,
	}
	ctx := SetAdminContextClaims(r.Context(), claims)
	next.ServeHTTP(w, r.WithContext(tenancy.WithContext(ctx, tenant)))
}
