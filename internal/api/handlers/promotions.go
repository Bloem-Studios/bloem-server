package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"

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
// pre-playback surfaces (docs/architecture/playback-overlays.md). Home
// cards ride on the `promoted` home section instead.
type PromotionsHandler struct {
	source promotionSource
	inbox  *notifications.System
}

// NewPromotionsHandler creates the handler; nil-safe when no service is wired.
func NewPromotionsHandler(svc *promotions.Service, inbox ...*notifications.System) *PromotionsHandler {
	h := &PromotionsHandler{}
	if len(inbox) > 0 {
		h.inbox = inbox[0]
	}
	if svc != nil {
		h.source = svc
	}
	return h
}

// HandleNativeList handles GET /api/bloem/v2/promotions?surface=…&content_id=…
// for the home, detail, pre-playback and in-playback surfaces; the home
// surface also carries the row position. The response carries cards only:
// there is no timer or wait field, the client always keeps "continue to
// content" as the default action.
func (h *PromotionsHandler) HandleNativeList(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.source == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Promotions are not available")
		return
	}
	surface := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("surface")))
	if !promotions.IsSurface(surface) {
		writeError(w, http.StatusBadRequest, "bad_request", "surface must be home, detail, pre_playback or in_playback")
		return
	}
	viewer := promotionViewer(r)
	var cards []promotions.Card
	var position *int
	var err error
	if surface == promotions.SurfaceHome {
		home, ok := h.source.(promotionViewerSource)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Promotions are not available")
			return
		}
		var at int
		cards, at, err = home.ActiveHome(r.Context(), viewer)
		position = &at
	} else {
		cards, err = h.source.Active(r.Context(), promotions.Query{
			Surface:   surface,
			ContentID: strings.TrimSpace(r.URL.Query().Get("content_id")),
			Viewer:    viewer,
		})
	}
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
	writeJSON(w, http.StatusOK, BloemPromotionsResponse{Surface: surface, Promotions: cards, HomePosition: position})
}

// promotionViewer is the profile a campaign request is evaluated for.
func promotionViewer(r *http.Request) promotions.Viewer {
	viewer := promotions.Viewer{UserID: apimw.GetUserID(r.Context()), ProfileID: apimw.GetProfileID(r.Context())}
	if scope, ok := access.GetScope(r.Context()); ok {
		viewer.LibraryIDs = scope.AllowedLibraryIDs
	}
	return viewer
}

// HandleSave sends only the authenticated profile's eligible campaign to its inbox.
// The deterministic delivery ID makes retries safe across server instances.
func (h *PromotionsHandler) HandleSave(w http.ResponseWriter, r *http.Request) {
	if h.source == nil || h.inbox == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Inbox is unavailable")
		return
	}
	viewer := promotionViewer(r)
	cards, err := h.source.Active(r.Context(), promotions.Query{Surface: promotions.SurfaceInPlayback, ContentID: strings.TrimSpace(r.URL.Query().Get("content_id")), Viewer: viewer})
	if err != nil {
		writeError(w, http.StatusBadRequest, "unavailable", "Campaign cannot be saved")
		return
	}
	for _, card := range cards {
		if card.ID != chi.URLParam(r, "id") {
			continue
		}
		if card.CTA == nil {
			break
		}
		body, err := json.Marshal(notifications.AlertBody{Title: card.Headline, Body: card.Subtitle, ImageURL: card.ImageURL, Severity: notifications.SeverityInfo, Dismissible: true, CTA: &notifications.AlertCTA{Label: card.CTA.Label, URL: card.CTA.URL}, ExpiresAt: &card.ExpiresAt})
		if err != nil {
			writeError(w, 500, "internal_error", "Could not save campaign")
			return
		}
		sum := sha256.Sum256([]byte("playback-save:" + viewer.ProfileID + ":" + card.ID))
		var id ulid.ULID
		copy(id[:], sum[:16])
		_, err = h.inbox.DispatchOperational(r.Context(), notifications.Delivery{ID: id.String(), UserID: viewer.UserID, ProfileID: viewer.ProfileID, Type: notifications.DeliveryTypeSystemAnnouncement, Body: body}, notifications.OperationalDispatch{})
		if err != nil {
			writeError(w, 500, "internal_error", "Could not save campaign")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "saved"})
		return
	}
	writeError(w, 404, "not_found", "Campaign is no longer available")
}
