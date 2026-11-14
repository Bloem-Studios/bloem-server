package handlers

// Bloem-owned. Break-glass support access: a platform administrator who is
// not a member of a customer's organization opens it by stating a reason.
//
// What the caller learns is deliberately no more than today's mint tells them:
// an account that is not the server Owner, an organization that does not exist
// and an organization the caller may not open are all the same 404. The
// operator is already known to be the Owner before the reason is judged, and
// the organization is confirmed to exist and be active before a missing reason
// is reported, so the 422 discloses nothing the Owner cannot already list.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/supportaccess"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// SupportAccessStore is the break-glass record the mint and the close route use.
// *supportaccess.Store satisfies it.
type SupportAccessStore interface {
	Eligible(ctx context.Context, accountID int) (bool, error)
	Open(ctx context.Context, in supportaccess.OpenInput) (supportaccess.Access, error)
	Validate(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID string) (supportaccess.Access, int64, error)
	Close(ctx context.Context, accessID, organizationID uuid.UUID, accountID int, loginSessionID, requestID string) error
}

// SetSupportAccess enables break-glass access on the mint.
func (h *AdminContextSessionHandler) SetSupportAccess(store SupportAccessStore) {
	if h != nil {
		h.support = store
	}
}

func (h *AdminContextSessionHandler) openSupport(w http.ResponseWriter, r *http.Request, claims *auth.Claims, incarnation uuid.UUID, organizationID uuid.UUID, request adminContextSupportRequest) {
	ctx := r.Context()
	eligible, err := h.support.Eligible(ctx, claims.UserID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	if !eligible {
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
		return
	}
	organization, err := h.memberships.GetOrganization(ctx, organizationID)
	if err != nil || organization.ID != organizationID {
		writeAdminContextSessionOrganizationError(w, err)
		return
	}
	switch {
	case organization.Status == tenancy.OrganizationSuspended:
		writeError(w, http.StatusForbidden, "organization_suspended", "Tenant access is suspended")
		return
	case organization.Status != tenancy.OrganizationActive || organization.OwnerAccountID == nil:
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
		return
	}

	access, ok := h.supportAccess(w, r, claims, organizationID, request)
	if !ok {
		return
	}

	expires := access.ExpiresAt
	h.mint(w, auth.AdminContextClaims{
		AccountID: claims.UserID, AccountIncarnationID: incarnation, SessionID: claims.SessionID, Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID, SupportAccessID: access.ID, PolicyRevision: organization.PolicyRevision,
		EffectiveAuthority: "platform_admin", ExpiresAt: expires,
	}, adminContextSummary{
		Key: "organization:" + organizationID.String(), Scope: auth.AdminScopeOrganization,
		OrganizationID: organizationID.String(), Name: organization.Name, Status: string(organization.Status), Authority: "platform_admin",
		PolicyRevision: organization.PolicyRevision, AccessBasis: "support", SupportAccessID: access.ID.String(), SupportExpiresAt: &expires,
	})
}

// HandleCloseSupportAccess handles DELETE /admin/organization/support-access:
// it ends the support access the pass was opened under, and with it the pass.
func (h *BloemOrganizationSupportHandler) HandleCloseSupportAccess(w http.ResponseWriter, r *http.Request) {
	claims, claimsOK := middleware.GetAdminContextClaims(r.Context())
	if !claimsOK || h == nil || h.access == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	if claims.Scope != auth.AdminScopeOrganization || claims.SupportAccessID == uuid.Nil {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	err := h.access.Close(r.Context(), claims.SupportAccessID, claims.OrganizationID, claims.AccountID, claims.SessionID, adminRequestID(r))
	if errors.Is(err, supportaccess.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Administrative resource not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant administration is unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetSupportAccess gives the support handler the break-glass record for its
// close route.
func (h *BloemOrganizationSupportHandler) SetSupportAccess(store SupportAccessStore) {
	if h != nil {
		h.access = store
	}
}

// supportAccess renews the access a request names, or opens a new one for the
// reason it states. It writes the answer itself when it cannot.
func (h *AdminContextSessionHandler) supportAccess(w http.ResponseWriter, r *http.Request, claims *auth.Claims, organizationID uuid.UUID, request adminContextSupportRequest) (supportaccess.Access, bool) {
	if request.AccessID != "" {
		return h.renewSupport(w, r, claims, organizationID, request)
	}
	reason, err := supportaccess.ValidateReason(request.Reason)
	if err != nil {
		writeAdminValidation(w, map[string]string{"support.reason": "must state why you are opening this organization, 10 to 500 characters without control characters"})
		return supportaccess.Access{}, false
	}
	access, err := h.support.Open(r.Context(), supportaccess.OpenInput{
		OrganizationID: organizationID, ActorAccountID: claims.UserID, LoginSessionID: claims.SessionID,
		Reason: reason, RequestID: adminRequestID(r),
	})
	var limited *supportaccess.RateLimitedError
	switch {
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many support accesses opened; try again later")
	case errors.Is(err, supportaccess.ErrNotEligible), errors.Is(err, supportaccess.ErrOrganizationUnavailable):
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	default:
		return access, true
	}
	return supportaccess.Access{}, false
}

// renewSupport confirms an access this login session opened is still open.
func (h *AdminContextSessionHandler) renewSupport(w http.ResponseWriter, r *http.Request, claims *auth.Claims, organizationID uuid.UUID, request adminContextSupportRequest) (supportaccess.Access, bool) {
	accessID, err := uuid.Parse(request.AccessID)
	if err != nil || accessID == uuid.Nil || request.Reason != "" {
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
		return supportaccess.Access{}, false
	}
	access, _, err := h.support.Validate(r.Context(), accessID, organizationID, claims.UserID, claims.SessionID)
	switch {
	case errors.Is(err, supportaccess.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	default:
		return access, true
	}
	return supportaccess.Access{}, false
}
