package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/ambience"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type bloemSeasonalViewerSource interface {
	ActiveForBloemViewer(context.Context, int, uuid.UUID) ([]ambience.Wire, error)
}

type BloemSeasonalViewerProfiles interface {
	ProfileOrganization(context.Context, int, string) (uuid.UUID, error)
}

// BloemSeasonalViewerHandler serves presentation packs to an authenticated,
// verified profile in its current tenant. Public login branding stays separate.
type BloemSeasonalViewerHandler struct {
	source   bloemSeasonalViewerSource
	profiles BloemSeasonalViewerProfiles
}

func NewBloemSeasonalViewerHandler(service *ambience.Service, profiles BloemSeasonalViewerProfiles) *BloemSeasonalViewerHandler {
	if service == nil || profiles == nil {
		return nil
	}
	return &BloemSeasonalViewerHandler{source: service, profiles: profiles}
}

// HandleGet requires account authentication, tenant resolution, viewer/PIN
// resolution and RequireProfile middleware. The consistency checks also fail
// closed if a required resolver is accidentally omitted from the route.
func (h *BloemSeasonalViewerHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	claims := apimw.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	tenant, hasTenant := tenancy.FromContext(r.Context())
	profileID := apimw.GetProfileID(r.Context())
	scope, hasScope := access.GetScope(r.Context())
	if !hasTenant || tenant.AccountID != claims.UserID || tenant.OrganizationID == uuid.Nil ||
		tenant.MembershipID == uuid.Nil || tenant.OrganizationStatus != tenancy.OrganizationActive ||
		tenant.MembershipStatus != tenancy.MembershipActive || tenant.PolicyRevision <= 0 || tenant.SecurityRevision <= 0 ||
		!hasScope || scope.UserID != claims.UserID || profileID == "" || scope.ProfileID != profileID || !scope.ProfileVerified {
		writeError(w, http.StatusForbidden, "viewer_required", "A verified viewer in an active organization is required")
		return
	}
	if h == nil || h.source == nil || h.profiles == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Seasonal presentation is unavailable")
		return
	}
	profileOrganization, err := h.profiles.ProfileOrganization(r.Context(), claims.UserID, profileID)
	if errors.Is(err, tenancy.ErrTenantNotFoundOrHidden) || (err == nil && profileOrganization != tenant.OrganizationID) {
		writeError(w, http.StatusForbidden, "viewer_required", "The profile is not available in this organization")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Seasonal presentation is unavailable")
		return
	}
	packs, err := h.source.ActiveForBloemViewer(r.Context(), claims.UserID, tenant.OrganizationID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Seasonal presentation is unavailable")
		return
	}
	if packs == nil {
		packs = []ambience.Wire{}
	}
	writeJSON(w, http.StatusOK, struct {
		Ambience []ambience.Wire `json:"ambience"`
	}{Ambience: packs})
}
