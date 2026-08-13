package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type adminContextSessionRequest struct {
	Scope          auth.AdminScope `json:"scope"`
	OrganizationID string          `json:"organization_id,omitempty"`
}

type adminContextSessionResponse struct {
	AccessToken string              `json:"access_token"`
	ExpiresAt   time.Time           `json:"expires_at"`
	Context     adminContextSummary `json:"context"`
}

type adminContextSummary struct {
	Key              string          `json:"key"`
	Scope            auth.AdminScope `json:"scope"`
	OrganizationID   string          `json:"organization_id,omitempty"`
	MembershipID     string          `json:"membership_id,omitempty"`
	Name             string          `json:"name"`
	Status           string          `json:"status"`
	Authority        string          `json:"authority"`
	PolicyRevision   int64           `json:"policy_revision,omitempty"`
	SecurityRevision int64           `json:"security_revision,omitempty"`
}

// AdminContextSessionStore reads the durable membership data used when a
// caller selects Organization scope. The ID is never accepted from the body.
type AdminContextSessionStore interface {
	GetMembership(context.Context, int, uuid.UUID) (tenancy.Membership, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type AdminContextSessionResolver interface {
	Resolve(context.Context, int, *uuid.UUID, bool) (tenancy.Context, error)
}

// AdminContextSessionHandler exchanges an authenticated account session for a
// separate short-lived administrative context token.
type AdminContextSessionHandler struct {
	tokens      auth.AdminContextTokenService
	resolver    AdminContextSessionResolver
	memberships AdminContextSessionStore
	platform    auth.PlatformAdminAuthorizer
}

func NewAdminContextSessionHandler(tokens auth.AdminContextTokenService, resolver AdminContextSessionResolver, memberships AdminContextSessionStore, platform auth.PlatformAdminAuthorizer) *AdminContextSessionHandler {
	return &AdminContextSessionHandler{tokens: tokens, resolver: resolver, memberships: memberships, platform: platform}
}

func (h *AdminContextSessionHandler) HandleSession(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if h == nil || h.tokens == nil || h.resolver == nil || h.memberships == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}

	var request adminContextSessionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid administrative context request")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid administrative context request")
		return
	}

	switch request.Scope {
	case auth.AdminScopePlatform:
		if request.OrganizationID != "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Platform context cannot select an organization")
			return
		}
		platformAdmin, err := h.platformAdmin(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
			return
		}
		if !platformAdmin {
			writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required")
			return
		}
		h.mint(w, auth.AdminContextClaims{AccountID: claims.UserID, Scope: auth.AdminScopePlatform}, adminContextSummary{
			Key: "platform", Scope: auth.AdminScopePlatform, Name: "Platform", Status: "active", Authority: "platform_admin",
		})
	case auth.AdminScopeOrganization:
		organizationID, err := uuid.Parse(request.OrganizationID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Organization context requires an organization_id")
			return
		}
		resolved, err := h.resolver.Resolve(r.Context(), claims.UserID, &organizationID, false)
		if err != nil {
			writeAdminContextSessionResolveError(w, err)
			return
		}
		membership, err := h.memberships.GetMembership(r.Context(), claims.UserID, organizationID)
		if err != nil {
			writeAdminContextSessionMembershipError(w, err)
			return
		}
		if resolved.AccountID != claims.UserID || resolved.OrganizationID != organizationID || resolved.MembershipID != membership.ID ||
			resolved.PolicyRevision <= 0 || resolved.SecurityRevision <= 0 || membership.AccountID != claims.UserID ||
			membership.OrganizationID != organizationID || membership.Status != tenancy.MembershipActive {
			writeError(w, http.StatusUnauthorized, "authorization_state_stale", "Tenant authorization state is stale")
			return
		}
		organization, err := h.memberships.GetOrganization(r.Context(), organizationID)
		if err != nil || organization.ID != organizationID || organization.Status != tenancy.OrganizationActive {
			writeAdminContextSessionOrganizationError(w, err)
			return
		}
		authority := "organization_admin"
		if membership.LegacyRole != "admin" {
			platformAdmin, err := h.platformAdmin(r.Context(), claims.UserID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
				return
			}
			if !platformAdmin {
				writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
				return
			}
			authority = "platform_admin"
		}
		h.mint(w, auth.AdminContextClaims{
			AccountID: claims.UserID, Scope: auth.AdminScopeOrganization,
			OrganizationID: organizationID, MembershipID: membership.ID,
			PolicyRevision: resolved.PolicyRevision, SecurityRevision: resolved.SecurityRevision,
		}, adminContextSummary{
			Key: "organization:" + organizationID.String(), Scope: auth.AdminScopeOrganization,
			OrganizationID: organizationID.String(), MembershipID: membership.ID.String(), Name: organization.Name, Status: string(organization.Status), Authority: authority,
			PolicyRevision: resolved.PolicyRevision, SecurityRevision: resolved.SecurityRevision,
		})
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "Unknown administrative context scope")
	}
}

func (h *AdminContextSessionHandler) platformAdmin(ctx context.Context, accountID int) (bool, error) {
	if h == nil || h.platform == nil {
		return false, tenancy.ErrTenantUnavailable
	}
	return h.platform.IsPlatformAdmin(ctx, accountID)
}

func (h *AdminContextSessionHandler) mint(w http.ResponseWriter, claims auth.AdminContextClaims, summary adminContextSummary) {
	token, err := h.tokens.Mint(claims)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	parsed, err := h.tokens.Parse(token)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, adminContextSessionResponse{AccessToken: token, ExpiresAt: parsed.ExpiresAt, Context: summary})
}

func writeAdminContextSessionResolveError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenancy.ErrTenantSuspended):
		writeError(w, http.StatusForbidden, "organization_suspended", "Tenant access is suspended")
	case errors.Is(err, tenancy.ErrTenantNotFoundOrHidden), errors.Is(err, tenancy.ErrOwnershipResolutionRequired):
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
	default:
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
	}
}

func writeAdminContextSessionMembershipError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenancy.ErrMembershipNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
}

func writeAdminContextSessionOrganizationError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenancy.ErrOrganizationNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Organization not found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
}
