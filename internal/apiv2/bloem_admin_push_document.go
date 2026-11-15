package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// The routes the Bloem admin apps use for push notifications. Handlers are in
// internal/api/handlers/bloem_admin_push.go. They exist only on a Bloem server
// with push delivery on, and the capability token admin_push_v1 says so; a
// plain Silo server has none of them.

const (
	bloemAdminPushLogin   = "Requires a signed-in administrator's account access token with a login session. API keys, impersonation and administrative-context tokens are refused. "
	bloemAdminPushDisplay = "Authenticated by the device's display token, not by a login: the iOS notification extension reads text while the phone is locked. The token opens only this device's own notifications and retire call. "
)

type BloemAdminPushDeviceInput struct {
	DeviceID string `path:"device_id" minLength:"1" doc:"The device_id a registration returned."`
}

type BloemAdminPushRegisterInput struct {
	Body handlers.BloemAdminPushRegisterRequest
}

type BloemAdminPushRegisterOutput struct {
	Body handlers.BloemAdminPushRegistered
}

type BloemAdminPushKindsInput struct {
	DeviceID string `path:"device_id" minLength:"1" doc:"The device_id a registration returned."`
	Body     handlers.BloemAdminPushKindsRequest
}

type BloemAdminPushKindsOutput struct {
	Body handlers.BloemAdminPushKinds
}

type BloemAdminPushContentInput struct {
	DeliveryID string `path:"delivery_id" minLength:"1" doc:"The delivery id a push carried, with or without its {device_id}. prefix."`
}

type BloemAdminPushContentOutput struct {
	Body handlers.BloemAdminPushContent
}

func registerBloemAdminPushDocument(reg *Registry) {
	noStore := map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	unavailable := "admin_push_unavailable: push delivery is off on this server, or it has no push backing."

	register := bloemChiDocumentOp(reg, "POST", "/admin/push/devices", "registerBloemAdminPushDevice", "admin-push",
		"Register an admin app installation for push.", "bloemAccountSession", http.StatusOK)
	register.Description = bloemAdminPushLogin + "Registers or refreshes the installation named by installation_id and returns the device id and a display token. Registering again with the same installation_id keeps the device id and per-kind switches, replaces the token and mints a new display token. A token already held by another account or installation moves to this one. The Apple topic must be com.bloemstudios.admin."
	register.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &register, map[int]string{
		400: "invalid_request: the body is not valid JSON for this route, or exceeds 32 KiB.",
		401: "unauthorized: no valid access token.",
		403: "admin_required: the account is not an enabled administrator; account_session_required: the token is an API key, impersonation or administrative-context token.",
		422: "invalid_registration: a field is missing or malformed, the topic is not com.bloemstudios.admin, or a kind id is unknown.",
		503: unavailable,
	})
	register.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK), Headers: noStore}
	registerBloemChiDocument[BloemAdminPushRegisterInput, BloemAdminPushRegisterOutput](reg, register)

	kinds := bloemChiDocumentOp(reg, "PATCH", "/admin/push/devices/{device_id}/kinds", "setBloemAdminPushKinds", "admin-push",
		"Switch notification kinds on or off for one device.", "bloemAccountSession", http.StatusOK)
	kinds.Description = bloemAdminPushLogin + "The server stops sending a switched-off kind to this device, including notifications already queued. Kinds not named keep their setting. The two reachability kinds are produced by the apps' own checks; their switches are stored here so they survive a reinstall."
	kinds.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &kinds, map[int]string{
		400: "invalid_request: the body is not valid JSON for this route.",
		401: "unauthorized: no valid access token.",
		403: "admin_required or account_session_required, as for registration.",
		404: "not_found: no device with this id belongs to the account.",
		422: "invalid_registration: a kind id is unknown.",
		503: unavailable,
	})
	kinds.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK), Headers: noStore}
	registerBloemChiDocument[BloemAdminPushKindsInput, BloemAdminPushKindsOutput](reg, kinds)

	remove := bloemChiDocumentOp(reg, "DELETE", "/admin/push/devices/{device_id}", "deleteBloemAdminPushDevice", "admin-push",
		"Remove one of the account's devices.", "bloemAccountSession", http.StatusNoContent)
	remove.Description = bloemAdminPushLogin + "Stops every push to the device. Use the retire route when no login is available."
	remove.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &remove, map[int]string{
		401: "unauthorized: no valid access token.",
		403: "admin_required or account_session_required, as for registration.",
		404: "not_found: no device with this id belongs to the account.",
		503: unavailable,
	})
	remove.Responses["204"] = &huma.Response{Description: http.StatusText(http.StatusNoContent), Headers: noStore}
	registerBloemChiDocument[BloemAdminPushDeviceInput, struct{}](reg, remove)

	retire := bloemChiDocumentOp(reg, "POST", "/admin/push/devices/{device_id}/retire", "retireBloemAdminPushDevice", "admin-push",
		"Stop pushes to a device using its display token.", "bloemAdminPushDisplayToken", http.StatusNoContent)
	retire.Description = bloemAdminPushDisplay + "Lets an app that has signed out, or whose session expired, remove its device."
	retire.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &retire, map[int]string{
		401: "invalid_display_token: the token is missing, unknown, replaced by a newer registration, or not this device's.",
		503: unavailable,
	})
	retire.Responses["204"] = &huma.Response{Description: http.StatusText(http.StatusNoContent), Headers: noStore}
	registerBloemChiDocument[BloemAdminPushDeviceInput, struct{}](reg, retire)

	content := bloemChiDocumentOp(reg, "GET", "/admin/push/content/{delivery_id}", "getBloemAdminPushContent", "admin-push",
		"Read the text and behavior of a notification pushed to this device.", "bloemAdminPushDisplayToken", http.StatusOK)
	content.Description = bloemAdminPushDisplay + "The push itself carries only an id; the relay never sees the text. A notification is visible only to a device it was sent to, for the account it was written for, until it expires (24 hours), and only while the account is still an enabled administrator."
	content.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &content, map[int]string{
		401: "invalid_display_token: the token is missing, unknown or replaced by a newer registration.",
		403: "admin_required: the account is no longer an enabled administrator.",
		404: "not_found: no such notification for this device, or it expired.",
		503: unavailable,
	})
	content.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK), Headers: noStore}
	registerBloemChiDocument[BloemAdminPushContentInput, BloemAdminPushContentOutput](reg, content)
}
