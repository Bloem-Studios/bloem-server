package handlers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// AdminLiveActivityBackend is what the Live Activity routes need from the push service;
// the real one is *notifications.AdminPushService. A backend without it has no such
// routes and never advertises admin_live_activities_v1.
type AdminLiveActivityBackend interface {
	LiveActivitiesEnabled(ctx context.Context) bool
	RegisterLiveActivity(ctx context.Context, userID int, deviceID string, in notifications.LiveActivityRegistration) error
	EndLiveActivity(ctx context.Context, userID int, deviceID, activityID string) error
	SetLiveStart(ctx context.Context, userID int, deviceID string, token *string, kinds []string) error
}

// BloemAdminLiveActivityRequest registers one Live Activity the app runs.
type BloemAdminLiveActivityRequest struct {
	ActivityID  string `json:"activity_id" format:"uuid" doc:"A UUID the app makes for the activity. Registering the same id again replaces its push token, for example after the token rotated."`
	Kind        string `json:"kind" enum:"scan,backup"`
	SubjectID   string `json:"subject_id,omitempty" doc:"The library id of a scan. Omit for a backup."`
	Platform    string `json:"platform" enum:"ios,android" doc:"ios for an ActivityKit Live Activity, android for an ongoing notification. Must match the device's platform."`
	PushToken   string `json:"push_token,omitempty" doc:"The activity's APNs push token, hex (64 to 200 characters). Required for ios; omit for android."`
	Environment string `json:"environment,omitempty" enum:"production,sandbox" doc:"The APNs environment of the token. Required for ios; omit for android."`
}

// BloemAdminLiveStartRequest stores or clears a device's push-to-start token.
type BloemAdminLiveStartRequest struct {
	Token *string  `json:"token" nullable:"true" doc:"The device's ActivityKit push-to-start token, hex (64 to 200 characters), or null to clear it. Apple devices only."`
	Kinds []string `json:"kinds" enum:"scan,backup" doc:"The jobs the person allowed the server to start an activity for. Must be empty when token is null."`
}

// liveBackend returns the backend's Live Activity half, if it has one.
func liveBackend(backend AdminPushBackend) AdminLiveActivityBackend {
	live, _ := backend.(AdminLiveActivityBackend)
	return live
}

// LiveAvailable reports whether the Live Activity routes can be mounted.
func (h *BloemAdminPushHandler) LiveAvailable() bool { return h.Available() && h.live != nil }

// LiveReady reports whether a client should be told the server keeps Live Activities
// current: push is ready and the operator's switch is on. It is asked on every probe.
func (h *BloemAdminPushHandler) LiveReady(ctx context.Context) bool {
	return h.LiveAvailable() && h.Ready(ctx) && h.live.LiveActivitiesEnabled(ctx)
}

// HandleRegisterLiveActivity records a Live Activity on one of the caller's devices.
func (h *BloemAdminPushHandler) HandleRegisterLiveActivity(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	var in BloemAdminLiveActivityRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	err := h.live.RegisterLiveActivity(r.Context(), claims.UserID, chi.URLParam(r, "device_id"), notifications.LiveActivityRegistration{
		ActivityID: in.ActivityID, Kind: in.Kind, SubjectID: in.SubjectID, Platform: in.Platform,
		PushToken: in.PushToken, Environment: in.Environment,
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// HandleEndLiveActivity deletes a Live Activity and its token when the activity ends.
func (h *BloemAdminPushHandler) HandleEndLiveActivity(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	if err := h.live.EndLiveActivity(r.Context(), claims.UserID, chi.URLParam(r, "device_id"), chi.URLParam(r, "activity_id")); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleSetLiveStart stores or clears the device's push-to-start token and the kinds the
// person allowed.
func (h *BloemAdminPushHandler) HandleSetLiveStart(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.admin(w, r)
	if !ok {
		return
	}
	var in BloemAdminLiveStartRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	if err := h.live.SetLiveStart(r.Context(), claims.UserID, chi.URLParam(r, "device_id"), in.Token, in.Kinds); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
