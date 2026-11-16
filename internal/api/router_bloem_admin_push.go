package api

import (
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

func itoa(n int) string { return strconv.Itoa(n) }

// bloemAdminPushHandler builds the admin push routes' handler, or nil when this
// build has no push backing.
func bloemAdminPushHandler(deps Dependencies) *handlers.BloemAdminPushHandler {
	return handlers.NewBloemAdminPushHandler(deps.Notifications.BloemAdminPush())
}

// mountBloemAdminPush mounts the admin app push routes and, only when they are
// mounted, advertises admin_push_v1. A server that lacks them (plain Silo, or a
// Bloem build without push backing) therefore reports no such token and the apps
// hide their push settings.
//
// Registration, kind switches and removal need an administrator's login session.
// Reading a notification and retiring a device carry the device's display token
// and no login: the iOS notification extension runs while the phone is locked.
func mountBloemAdminPush(r chi.Router, h *handlers.BloemAdminPushHandler, system *handlers.BloemSystemHandler, authMW *apimw.AuthMiddleware) {
	if h == nil || !h.Available() || authMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(authMW.RequireAuth)
		r.Post("/admin/push/devices", h.HandleRegister)
		r.Patch("/admin/push/devices/{device_id}/kinds", h.HandleSetKinds)
		r.Put("/admin/push/devices/{device_id}/widgets", h.HandleSetWidgets)
		r.Delete("/admin/push/devices/{device_id}", h.HandleDelete)
	})
	r.Post("/admin/push/devices/{device_id}/retire", h.HandleRetire)
	r.Get("/admin/push/content/{delivery_id}", h.HandleContent)
	if system != nil {
		system.SetAdminPushReadiness(h.Ready)
	}
}
