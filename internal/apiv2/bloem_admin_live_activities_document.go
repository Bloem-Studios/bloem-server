package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// The routes the Bloem admin apps use to have the server keep their scan and backup
// Live Activities current. Handlers are in
// internal/api/handlers/bloem_admin_live_activities.go. They exist next to the admin
// push routes; an app uses them only on a server that lists admin_live_activities_v1,
// which it does only while the operator's switch is on.

const (
	bloemLiveNoLogin  = "unauthorized: no valid access token."
	bloemLiveNotAdmin = "admin_required or account_session_required, as for device registration."
	bloemLiveNoDevice = "not_found: no device with this id belongs to the account."
)

type BloemAdminLiveActivityInput struct {
	DeviceID string `path:"device_id" minLength:"1" doc:"The device_id a registration returned."`
	Body     handlers.BloemAdminLiveActivityRequest
}

type BloemAdminLiveActivityEndInput struct {
	DeviceID   string `path:"device_id" minLength:"1" doc:"The device_id a registration returned."`
	ActivityID string `path:"activity_id" format:"uuid" doc:"The activity_id the app registered."`
}

type BloemAdminLiveStartInput struct {
	DeviceID string `path:"device_id" minLength:"1" doc:"The device_id a registration returned."`
	Body     handlers.BloemAdminLiveStartRequest
}

func registerBloemAdminLiveActivitiesDocument(reg *Registry) {
	noStore := map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	unavailable := "admin_push_unavailable: push delivery or the operator's Live Activities switch is off on this server, or it has no push backing."
	privacy := "The server builds each update from ids, counts, state words and Unix-second times only; names come from the app. "

	register := bloemChiDocumentOp(reg, "POST", "/admin/push/devices/{device_id}/live-activities", "registerBloemAdminLiveActivity", "admin-push",
		"Register a scan or backup Live Activity the app runs.", "bloemAccountSession", http.StatusCreated)
	register.Description = bloemAdminPushLogin + privacy + "Use only on a server that lists admin_live_activities_v1. On iOS send the activity's push token and its environment; the server sends start, update and end events through the push relay's live_activity mode: at most one update every 30 seconds with the newest state, a change of phase and the end at once, and nothing after the end or 8 hours after registration. On Android send no token; the server sends an ordinary push whose delivery id is {device_id}.live-{activity_id}-{unix seconds}, and the app re-reads the job and redraws its ongoing notification. Registering the same activity_id again replaces its token. A device keeps at most five activities; a sixth replaces the oldest."
	register.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &register, map[int]string{
		400: "invalid_request: the body is not valid JSON for this route.",
		401: bloemLiveNoLogin,
		403: bloemLiveNotAdmin,
		404: bloemLiveNoDevice,
		422: "invalid_registration: a field is missing or malformed, the platform does not match the device, or the activity_id belongs to another device.",
		503: unavailable,
	})
	register.Responses["201"] = &huma.Response{Description: http.StatusText(http.StatusCreated), Headers: noStore}
	registerBloemChiDocument[BloemAdminLiveActivityInput, struct{}](reg, register)

	end := bloemChiDocumentOp(reg, "DELETE", "/admin/push/devices/{device_id}/live-activities/{activity_id}", "deleteBloemAdminLiveActivity", "admin-push",
		"Forget a Live Activity that ended.", "bloemAccountSession", http.StatusNoContent)
	end.Description = bloemAdminPushLogin + "Deletes the activity and its token; the server sends it nothing more. Call it when the activity ends on the phone. An activity the server no longer has is not an error."
	end.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &end, map[int]string{
		401: bloemLiveNoLogin,
		403: bloemLiveNotAdmin,
		404: bloemLiveNoDevice,
		422: "invalid_registration: activity_id is not a UUID.",
		503: unavailable,
	})
	end.Responses["204"] = &huma.Response{Description: http.StatusText(http.StatusNoContent), Headers: noStore}
	registerBloemChiDocument[BloemAdminLiveActivityEndInput, struct{}](reg, end)

	start := bloemChiDocumentOp(reg, "PUT", "/admin/push/devices/{device_id}/live-activity-start", "setBloemAdminLiveActivityStart", "admin-push",
		"Store or clear the device's push-to-start token.", "bloemAccountSession", http.StatusNoContent)
	start.Description = bloemAdminPushLogin + privacy + "Apple devices on iOS 17.2 or later. With a token and kinds, the server may start an activity on this device: for a scan that has run 60 seconds, and for a backup at once, and only while the device has the matching alert kind switched on (admin.scan_failed for scans, admin.backup_failed for backups). Each job is started once per device. Send token null and no kinds when the person turns the setting off; the server then starts nothing. Clearing works with the operator's switch off."
	start.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &start, map[int]string{
		400: "invalid_request: the body is not valid JSON for this route.",
		401: bloemLiveNoLogin,
		403: bloemLiveNotAdmin,
		404: bloemLiveNoDevice,
		422: "invalid_registration: the token is malformed, a kind is unknown, kinds were sent without a token, or the device is not an Apple device.",
		503: unavailable,
	})
	start.Responses["204"] = &huma.Response{Description: http.StatusText(http.StatusNoContent), Headers: noStore}
	registerBloemChiDocument[BloemAdminLiveStartInput, struct{}](reg, start)
}
