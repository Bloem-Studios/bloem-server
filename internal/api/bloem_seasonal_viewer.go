package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// mountBloemSeasonalViewer is called inside /api/bloem/v1 alongside the client
// surface. All three authority resolvers are mandatory; a partially configured
// server neither mounts nor advertises viewer delivery. RequireAuth retains the
// existing default-deny direct-profile route guard unchanged.
func mountBloemSeasonalViewer(r chi.Router, handler *handlers.BloemSeasonalViewerHandler, client bloemClientSurface, system *handlers.BloemSystemHandler) {
	available := handler != nil && client.auth != nil && client.tenant != nil && client.viewer != nil
	if system != nil {
		system.SetSeasonalViewerAvailable(available)
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
		r.Get("/ambience", handler.HandleGet)
	})
}
