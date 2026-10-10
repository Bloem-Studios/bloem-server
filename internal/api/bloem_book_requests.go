package api

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/taskmanager/tasks"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func newBloemBookRequests(deps Dependencies) *handlers.BloemBookRequestsHandler {
	if deps.DB == nil {
		return nil
	}
	var notifier bookrequests.Notifier
	if n := notifications.NewBookRequestNotifier(deps.Notifications); n != nil {
		notifier = n
	}
	service := bookrequests.NewService(deps.DB, notifier)
	if deps.TaskManager != nil {
		deps.TaskManager.Register(tasks.NewBookRequestsTask(service))
	}
	return handlers.NewBloemBookRequestsHandler(service, tenancy.NewStore(deps.DB))
}
func mountBloemBookRequests(r chi.Router, h *handlers.BloemBookRequestsHandler, client bloemClientSurface, system *handlers.BloemSystemHandler) {
	available := h != nil && client.auth != nil && client.tenant != nil && client.viewer != nil
	if system != nil {
		system.SetBookRequestsAvailable(available)
	}
	if !available {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(client.auth.RequireAuth)
		r.Use(client.tenant.ResolveNative)
		if client.rateLimit != nil {
			r.Use(client.rateLimit.Handler)
		}
		r.Use(client.viewer.RequireViewerAccess)
		r.Use(apimw.RequireProfile)
		r.Get("/book-requests/capability", h.HandleCapability)
		r.Get("/book-requests/mine", h.HandleMine)
		r.Post("/book-requests", h.HandleCreate)
		r.Get("/book-requests/{id}", h.HandleGet)
		r.Post("/book-requests/{id}/cancel", h.HandleCancel)
	})
}
func mountBloemAdminBookRequests(r chi.Router, h *handlers.BloemBookRequestsHandler) {
	if h == nil {
		return
	}
	r.Get("/organization/book-requests", h.HandleAdminList)
	// Route patterns stay explicit for contract and authority inventories.
	for _, action := range []string{"approve", "decline", "fulfil"} {
		r.Post("/organization/book-requests/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			chi.RouteContext(r.Context()).URLParams.Add("action", action)
			h.HandleAdminAction(w, r)
		})
	}
}
