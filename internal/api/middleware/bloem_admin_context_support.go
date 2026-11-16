package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// authorizeSupportAccess authorizes a request whose pass was opened under break-glass
// support access. Its holder is a platform administrator who is not a member
// of the organization, so there is no membership to revalidate. What is
// revalidated instead, on every request: the operator is still an enabled
// platform administrator, and the access is open, unexpired, opened from this
// login session for this organization, with the operator still the server
// Owner and the organization still active and owned. The tenant context it
// builds has no membership; the organization routes already require the claim
// and the context to agree, and both are empty.
func (m *AdminContextMiddleware) authorizeSupportAccess(ctx context.Context, claims auth.AdminContextClaims, operator auth.OperatorAuthority) (context.Context, *AdminContextRefusal) {
	stale := func() (context.Context, *AdminContextRefusal) {
		return ctx, refuse(http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
	}
	if !operator.PlatformAdmin || claims.EffectiveAuthority != "platform_admin" || m.supportAccess == nil {
		return stale()
	}
	policyRevision, err := m.supportAccess.ValidateSupportAccess(ctx, claims.SupportAccessID, claims.OrganizationID, claims.AccountID, claims.SessionID)
	if errors.Is(err, ErrSupportAccessInvalid) {
		return stale()
	}
	if err != nil {
		return ctx, refuse(http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
	if policyRevision != claims.PolicyRevision {
		return stale()
	}
	tenant := tenancy.Context{
		OrganizationID: claims.OrganizationID, AccountID: claims.AccountID,
		OrganizationStatus: tenancy.OrganizationActive, PolicyRevision: policyRevision,
	}
	return tenancy.WithContext(SetAdminContextClaims(ctx, claims), tenant), nil
}
