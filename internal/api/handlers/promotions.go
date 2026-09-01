package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

// promotionSource is the per-profile delivery seam; production is
// *promotions.Service, tests use a fake.
type promotionSource interface {
	Active(ctx context.Context, q promotions.Query) ([]promotions.Card, error)
}

// PromotionsHandler serves S-2 promotion cards for the detail and
// pre-playback surfaces (docs/specs/client-engagement.md section B.3). Home
// cards ride on the `promoted` home section instead.
type PromotionsHandler struct {
	source promotionSource
}

// NewPromotionsHandler creates the handler; nil-safe when no service is wired.
func NewPromotionsHandler(svc *promotions.Service) *PromotionsHandler {
	h := &PromotionsHandler{}
	if svc != nil {
		h.source = svc
	}
	return h
}

type promotionsResponse struct {
	Surface    string            `json:"surface"`
	Promotions []promotions.Card `json:"promotions"`
}

// HandleList handles GET /promotions?surface=detail|pre_playback&content_id=…
// The response carries cards only: there is no timer or wait field, the
// client always keeps "continue to content" as the default action.
func (h *PromotionsHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.source == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Promotions are not available")
		return
	}
	surface := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if surface != promotions.SurfaceDetail && surface != promotions.SurfacePrePlayback {
		writeError(w, http.StatusBadRequest, "bad_request", "surface must be detail or pre_playback")
		return
	}
	viewer := promotions.Viewer{UserID: apimw.GetUserID(r.Context()), ProfileID: apimw.GetProfileID(r.Context())}
	if scope, ok := access.GetScope(r.Context()); ok {
		viewer.LibraryIDs = scope.AllowedLibraryIDs
	}
	cards, err := h.source.Active(r.Context(), promotions.Query{
		Surface:   surface,
		ContentID: strings.TrimSpace(r.URL.Query().Get("content_id")),
		Viewer:    viewer,
	})
	if err != nil {
		if errors.Is(err, promotions.ErrInvalid) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load promotions")
		return
	}
	if cards == nil {
		cards = []promotions.Card{}
	}
	writeJSON(w, http.StatusOK, promotionsResponse{Surface: surface, Promotions: cards})
}
