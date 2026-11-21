package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/notifications"
)

// BloemAdminPushHandler serves the routes the Bloem admin apps use to receive
// push notifications. Registration, kind switches and removal take an
// administrator's login session; reading a notification and retiring a device
// take the device's display token, which opens nothing else.
type BloemAdminPushHandler struct {
	service AdminPushBackend
	// live is the backend's Live Activity half; nil when it has none.
	live AdminLiveActivityBackend
}

// AdminPushBackend is what the handler needs from the push service; the real
// one is *notifications.AdminPushService.
type AdminPushBackend interface {
	Available() bool
	Enabled(ctx context.Context) bool
	Register(ctx context.Context, userID int, reg notifications.AdminPushRegistration) (*notifications.AdminPushRegistered, error)
	SetKinds(ctx context.Context, userID int, deviceID string, kinds map[string]bool) ([]notifications.AdminPushKindState, error)
	Unregister(ctx context.Context, userID int, deviceID string) error
	SetWidgets(ctx context.Context, userID int, deviceID string, placed bool, channels []string) (bool, []string, error)
	Retire(ctx context.Context, displayToken, deviceID string) error
	Content(ctx context.Context, displayToken, deliveryID string) (*notifications.AdminPushContent, error)
}

// NewBloemAdminPushHandler wraps the service; nil when this build has none.
func NewBloemAdminPushHandler(service *notifications.AdminPushService) *BloemAdminPushHandler {
	if service == nil {
		return nil
	}
	return NewBloemAdminPushHandlerWith(service)
}

// NewBloemAdminPushHandlerWith wraps any backend.
func NewBloemAdminPushHandlerWith(backend AdminPushBackend) *BloemAdminPushHandler {
	if backend == nil {
		return nil
	}
	return &BloemAdminPushHandler{service: backend, live: liveBackend(backend)}
}

// Available reports whether the routes can be mounted at all.
func (h *BloemAdminPushHandler) Available() bool { return h != nil && h.service.Available() }

// Ready reports whether a client should be told push works: the routes exist and
// the operator has push delivery on. It is asked on every capability probe.
func (h *BloemAdminPushHandler) Ready(ctx context.Context) bool {
	return h.Available() && h.service.Enabled(ctx)
}

// BloemAdminPushRegisterRequest registers or refreshes one app installation.
type BloemAdminPushRegisterRequest struct {
	InstallationID  string    `json:"installation_id" minLength:"8" maxLength:"128" doc:"A key the app generates once per installation and keeps across token refreshes and sign-ins."`
	Platform        string    `json:"platform" enum:"apple,android"`
	Token           string    `json:"token" doc:"The APNs device token (hex) or the FCM registration token."`
	APNsEnvironment string    `json:"apns_environment,omitempty" enum:"production,sandbox" doc:"Required for apple."`
	APNsTopic       string    `json:"apns_topic,omitempty" doc:"Required for apple; must be com.bloemstudios.admin."`
	DisabledKinds   *[]string `json:"disabled_kinds,omitempty" doc:"Kind ids this device has switched off. Replaces the stored switches when present; omit to keep them."`
	WidgetsPlaced   *bool     `json:"widgets_placed,omitempty" doc:"Whether the app has at least one home-screen widget placed. Only then does the server send widget wakes. Omit to keep what the server holds. Reporting false clears the channels."`
	WidgetChannels  *[]string `json:"widget_channels,omitempty" enum:"sessions,requests,libraries,nodes,health" doc:"What the placed widgets show: sessions (now playing), requests, libraries, nodes, health (Needs a look: backup and plugin failures; only to a server that lists admin_widget_health_v1). Required, and not empty, when widgets_placed is true."`
}

// BloemAdminPushRegistered answers a registration.
type BloemAdminPushRegistered struct {
	DeviceID     string                             `json:"device_id" doc:"Prefixes every delivery id pushed to this device, so the app can tell which saved server a push came from: delivery_id is {device_id}.{delivery}."`
	DisplayToken string                             `json:"display_token" doc:"Bearer token for reading this device's notification text and retiring it. Returned by every registration; a new one replaces the last."`
	Kinds        []notifications.AdminPushKindState `json:"kinds"`
	// WidgetsPlaced and WidgetChannels are what the server now holds for this device.
	WidgetsPlaced  bool     `json:"widgets_placed"`
	WidgetChannels []string `json:"widget_channels"`
}

// BloemAdminPushWidgetsRequest reports the widgets an app has placed.
type BloemAdminPushWidgetsRequest struct {
	WidgetsPlaced bool     `json:"widgets_placed" doc:"Whether at least one widget is placed. Send false when the last widget is removed."`
	Channels      []string `json:"channels" enum:"sessions,requests,libraries,nodes,health" doc:"What the placed widgets show; health only to a server that lists admin_widget_health_v1. Required, and not empty, when widgets_placed is true; ignored when false."`
}

// BloemAdminPushWidgets echoes what the server holds.
type BloemAdminPushWidgets struct {
	WidgetsPlaced bool     `json:"widgets_placed"`
	Channels      []string `json:"channels"`
}

// BloemAdminPushKindsRequest switches kinds on or off for one device.
type BloemAdminPushKindsRequest struct {
	Kinds map[string]bool `json:"kinds" doc:"Kind id to enabled. Kinds not named keep their setting."`
}

// BloemAdminPushKinds is the device's switches.
type BloemAdminPushKinds struct {
	Kinds []notifications.AdminPushKindState `json:"kinds"`
}

// BloemAdminPushContent is one notification's text and behavior.
type BloemAdminPushContent struct {
	DeliveryID  string    `json:"delivery_id"`
	Kind        string    `json:"kind"`
	Channel     string    `json:"channel" enum:"servers,requests,libraries,backups,nodes,plugins"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	LockScreen  string    `json:"lock_screen" doc:"All a locked screen shows unless the device allows details."`
	Page        string    `json:"page,omitempty" doc:"App link page a tap opens: requests, libraries, backups, nodes or plugins."`
	ItemID      string    `json:"item_id,omitempty" doc:"The item on that page: a request id, library id, node id or plugin installation id."`
	SubjectType string    `json:"subject_type,omitempty"`
	Actions     []string  `json:"actions" enum:"approve,decline" doc:"Buttons the notification offers. Empty for most kinds."`
	Badge       *int      `json:"badge,omitempty" doc:"Waiting items to show on the app icon."`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *BloemAdminPushHandler) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notifications.ErrAdminPushUnavailable):
		writeError(w, http.StatusServiceUnavailable, "admin_push_unavailable", "Push notifications for administrators are unavailable on this server.")
	case errors.Is(err, notifications.ErrAdminPushInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_registration", err.Error())
	case errors.Is(err, notifications.ErrAdminPushNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such device or notification.")
	case errors.Is(err, notifications.ErrAdminPushUnauthorized):
		writeError(w, http.StatusUnauthorized, "invalid_display_token", "The display token is missing or no longer valid.")
	case errors.Is(err, notifications.ErrAdminPushForbidden):
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required.")
	default:
		writeError(w, http.StatusServiceUnavailable, "admin_push_unavailable", "Push notifications for administrators are unavailable on this server.")
	}
}

// admin returns the signed-in administrator. The routes accept only a real
// login session: not an API key, an impersonation, or an administrative-context
// token, none of which belong to one phone.
func (h *BloemAdminPushHandler) admin(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	w.Header().Set("Cache-Control", "no-store")
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return nil, false
	}
	if claims.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin_required", "Administrator access is required.")
		return nil, false
	}
	if claims.TokenType != auth.TokenTypeAccess || claims.SessionID == "" || claims.ImpersonatorUserID != nil {
		writeError(w, http.StatusForbidden, "account_session_required", "Sign in as the administrator to register a device.")
		return nil, false
	}
	return claims, true
}

func displayToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func decodeStrict(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON for this route.")
		return false
	}
	return true
}

func (h *BloemAdminPushHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	var in BloemAdminPushRegisterRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	out, err := h.service.Register(r.Context(), claims.UserID, notifications.AdminPushRegistration{
		InstallationID: in.InstallationID, Platform: in.Platform, Token: in.Token,
		APNsEnvironment: in.APNsEnvironment, APNsTopic: in.APNsTopic, DisabledKinds: in.DisabledKinds,
		WidgetsPlaced: in.WidgetsPlaced, WidgetChannels: in.WidgetChannels,
		LoginSessionID: claims.SessionID,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemAdminPushRegistered{
		DeviceID: out.DeviceID, DisplayToken: out.DisplayToken, Kinds: out.Kinds,
		WidgetsPlaced: out.WidgetsPlaced, WidgetChannels: out.WidgetChannels,
	})
}

func (h *BloemAdminPushHandler) HandleSetKinds(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	var in BloemAdminPushKindsRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	kinds, err := h.service.SetKinds(r.Context(), claims.UserID, chi.URLParam(r, "device_id"), in.Kinds)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemAdminPushKinds{Kinds: kinds})
}

// HandleSetWidgets records which widget channels the app's placed widgets show. The app calls it
// whenever a widget is added or removed.
func (h *BloemAdminPushHandler) HandleSetWidgets(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	var in BloemAdminPushWidgetsRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	placed, channels, err := h.service.SetWidgets(r.Context(), claims.UserID, chi.URLParam(r, "device_id"), in.WidgetsPlaced, in.Channels)
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, BloemAdminPushWidgets{WidgetsPlaced: placed, Channels: channels})
}

func (h *BloemAdminPushHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	if err := h.service.Unregister(r.Context(), claims.UserID, chi.URLParam(r, "device_id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleRetire removes a device on its display token alone, so an app that has
// already signed out, or whose session expired, can still stop its pushes.
func (h *BloemAdminPushHandler) HandleRetire(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if err := h.service.Retire(r.Context(), displayToken(r), chi.URLParam(r, "device_id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleContent returns the text of one notification to the device it was sent to.
func (h *BloemAdminPushHandler) HandleContent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	content, err := h.service.Content(r.Context(), displayToken(r), chi.URLParam(r, "delivery_id"))
	if err != nil {
		h.fail(w, err)
		return
	}
	actions := content.Actions
	if actions == nil {
		actions = []string{}
	}
	writeJSON(w, http.StatusOK, BloemAdminPushContent{
		DeliveryID: content.DeliveryID, Kind: content.Kind, Channel: content.Channel, Title: content.Title, Body: content.Body,
		LockScreen: content.LockScreen, Page: content.Page, ItemID: content.ItemID, SubjectType: content.SubjectType,
		Actions: actions, Badge: content.Badge, CreatedAt: content.CreatedAt,
	})
}
