package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/ambience"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/go-chi/chi/v5"
)

// notificationReadPayload is the payload published on the notifications
// events channel when notifications are marked read. It was an inline
// map[string]any literal built conditionally, which had no nameable type for
// the client DTO registry (contracts/client/v1/registry.json). The map
// marshaled its keys in sorted order and omitted absent ones; the struct
// declares the fields in that same order with the same omitempty behavior so
// the bytes are unchanged: "id" set means one notification, "all" means every
// notification, never both.
type notificationReadPayload struct {
	All       bool   `json:"all,omitempty"`
	ID        string `json:"id,omitempty"`
	ProfileID string `json:"profile_id"`
}

// notificationDismissedPayload is the payload published on the notifications
// events channel when a notification is dismissed. It was an inline
// map[string]any literal, which had no nameable type for the client DTO
// registry (contracts/client/v1/registry.json). The map marshaled its keys
// in sorted order; the struct declares the fields in that same order so the
// bytes are unchanged.
type notificationDismissedPayload struct {
	ID        string `json:"id"`
	ProfileID string `json:"profile_id"`
}

// parseIncludeDismissed reads the `include_dismissed` query flag shared by
// the list and sync endpoints. Expired rows are never served regardless.
func parseIncludeDismissed(r *http.Request) bool {
	switch r.URL.Query().Get("include_dismissed") {
	case "1", "true":
		return true
	}
	return false
}

// deliveryDismisser is the repository seam HandleDismiss uses; tests
// substitute a fake, production uses System.Deliveries.
type deliveryDismisser interface {
	Dismiss(ctx context.Context, profileID, id string) (bool, error)
	Exists(ctx context.Context, profileID, id string) (bool, error)
}

func (h *NotificationsHandler) dismisser() deliveryDismisser {
	if h.dismissStore != nil {
		return h.dismissStore
	}
	return h.system.Deliveries
}

// HandleDismiss handles POST /notifications/{id}/dismiss. Dismiss is
// distinct from read: it hides the alert banner/row from feeds (unless the
// client asks for include_dismissed) and leaves read state alone. Critical
// alerts (dismissible=false) answer 409.
func (h *NotificationsHandler) HandleDismiss(w http.ResponseWriter, r *http.Request) {
	userID := apimw.GetUserID(r.Context())
	profileID := apimw.GetProfileID(r.Context())
	id := chi.URLParam(r, "id")
	store := h.dismisser()

	transitioned, err := store.Dismiss(r.Context(), profileID, id)
	if errors.Is(err, notifications.ErrDeliveryNotDismissible) {
		writeError(w, http.StatusConflict, "not_dismissible", "This notification cannot be dismissed")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to dismiss notification")
		return
	}
	if !transitioned {
		exists, err := store.Exists(r.Context(), profileID, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to dismiss notification")
			return
		}
		if !exists {
			writeError(w, http.StatusNotFound, "not_found", "Notification not found")
			return
		}
	}
	if transitioned && h.hub != nil {
		_ = h.hub.PublishJSON(r.Context(), evt.ChannelNotifications, notifications.EventNotificationDismissed,
			notificationDismissedPayload{ID: id, ProfileID: profileID},
			evt.PublishOptions{UserID: userID, ProfileID: profileID})
	}
	w.WriteHeader(http.StatusNoContent)
}

type capabilityRemoteControl struct {
	Admin     bool `json:"admin"`
	Household bool `json:"household"`
}

// capabilityPromotions advertises the S-2 delivery surfaces.
type capabilityPromotions struct {
	PlaybackOverlay bool     `json:"playback_overlay"`
	Surfaces        []string `json:"surfaces"`
}

// SetPromotions advertises the S-2 promotions capability.
func (h *NotificationsHandler) SetPromotions(enabled bool) { h.promotions = enabled }

// ambienceAccountSource supplies the active packs visible to an account.
type ambienceAccountSource interface {
	ActiveForAccount(ctx context.Context, accountID int) ([]ambience.Wire, error)
}

// SetAmbience wires the S-3 pack registry into the capability payload.
func (h *NotificationsHandler) SetAmbience(src ambienceAccountSource) { h.ambience = src }

// bloemNotificationsHandlerExt holds Bloem-only NotificationsHandler
// dependencies advertised on the capability payload.
type bloemNotificationsHandlerExt struct {
	// ambience is the optional S-3 pack registry echoed on the capability payload.
	ambience ambienceAccountSource
	// promotions advertises the S-2 delivery surfaces on the capability payload.
	promotions bool
}

// bloemDecorateCapabilities fills the Bloem capability fields (S-1
// announcements and dismiss, S-3 ambience, S-2 promotions, S-5a remote
// control) on the notification capability payload.
func (h *NotificationsHandler) bloemDecorateCapabilities(ctx context.Context, resp *capabilityResponse) {
	var ambienceBlock *[]ambience.Wire
	if h.ambience != nil {
		active := []ambience.Wire{}
		if packs, err := h.ambience.ActiveForAccount(ctx, apimw.GetUserID(ctx)); err == nil && packs != nil {
			active = packs
		}
		ambienceBlock = &active
	}
	var promotionsBlock *capabilityPromotions
	if h.promotions {
		promotionsBlock = &capabilityPromotions{Surfaces: promotions.Surfaces, PlaybackOverlay: true}
	}
	// Announcements are a server feature, not a per-profile setting:
	// advertise them whenever the system runs (the admin compose route
	// is mounted under the same condition).
	resp.Announcements = true
	resp.SupportedTypes = notifications.SupportedDeliveryTypes()
	resp.Dismiss = true
	resp.Ambience = ambienceBlock
	resp.Promotions = promotionsBlock
	resp.RemoteControl = capabilityRemoteControl{Admin: true, Household: true}
}
