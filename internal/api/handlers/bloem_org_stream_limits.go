package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/bloeminsights"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// OrgStreamLimitsCapabilityToken says this server serves the organization
// limits and stats routes.
const OrgStreamLimitsCapabilityToken = "admin_org_stream_limits_v1"

// MountOrganization mounts the organization routes on r, a router whose
// requests carry admin context claims. They show an organization's admins
// their own members' limit use, refusals and viewing stats, and never a
// location, edge, Cloudflare colo or transport.
func (h *BloemStreamInsightsHandler) MountOrganization(r chi.Router) {
	if !h.Available() {
		return
	}
	r.Get("/organization/limits/usage", h.organization(h.handleUsage))
	r.Get("/organization/limits/refusals", h.organization(h.handleRefusals))
	r.Get("/organization/stats/top-people", h.organization(h.handleTopPeople))
	r.Get("/organization/stats/heatmap", h.organization(h.handleHeatmap))
	r.Get("/organization/stats/clients", h.organization(h.handleClients))
	r.Get("/organization/stats/transcode-causes", h.organization(h.handleTranscodeCauses))
}

// organization accepts only an organization admin context whose claims
// match the resolved tenant, as the organization support routes do, and
// scopes the query to that organization.
func (h *BloemStreamInsightsHandler) organization(next scopedInsightsHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, claimsOK := middleware.GetAdminContextClaims(r.Context())
		tenant, tenantOK := tenancy.FromContext(r.Context())
		if !claimsOK || !tenantOK || claims.Scope != auth.AdminScopeOrganization || claims.AccountID <= 0 || claims.OrganizationID == uuid.Nil ||
			claims.AccountID != tenant.AccountID || claims.OrganizationID != tenant.OrganizationID || claims.MembershipID != tenant.MembershipID {
			writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
			return
		}
		next(w, r, bloeminsights.Scope{OrganizationID: tenant.OrganizationID})
	}
}
