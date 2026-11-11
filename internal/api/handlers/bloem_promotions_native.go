package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/promotions"
)

// promotionViewerSource is what the native viewer routes need beyond
// promotionSource: home delivery with its row position, and dismissals.
// Production is *promotions.Service.
type promotionViewerSource interface {
	ActiveHome(ctx context.Context, viewer promotions.Viewer) ([]promotions.Card, int, error)
	Dismiss(ctx context.Context, viewer promotions.Viewer, surface, id string) error
}

// BloemPromotionsResponse is the native viewer campaign list.
type BloemPromotionsResponse struct {
	Surface    string            `json:"surface" doc:"The surface the cards are for."`
	Promotions []promotions.Card `json:"promotions" nullable:"false" doc:"Cards to show, in order. Empty when none apply."`
	// HomePosition is set for the home surface only.
	HomePosition *int `json:"home_position,omitempty" doc:"Home surface only: the index among the profile's home rows at which the campaign row is shown. A value past the last row means after the last row."`
}

// HandleDismiss handles PUT /api/bloem/v2/promotions/{id}/dismissals/{surface}:
// the profile does not want this campaign on that surface again. It is
// idempotent and answers 204.
func (h *PromotionsHandler) HandleDismiss(w http.ResponseWriter, r *http.Request) {
	var dismisser promotionViewerSource
	if h != nil {
		dismisser, _ = h.source.(promotionViewerSource)
	}
	if dismisser == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Promotions are not available")
		return
	}
	surface := strings.ToLower(chi.URLParam(r, "surface"))
	err := dismisser.Dismiss(r.Context(), promotionViewer(r), surface, chi.URLParam(r, "id"))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, promotions.ErrInvalid):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, promotions.ErrDismissalUnavailable):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Dismissals are not available")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to save dismissal")
	}
}
