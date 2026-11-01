package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// bloemPromotionsViewerHandler builds the native viewer campaign handler; nil
// without a campaign registry.
func bloemPromotionsViewerHandler(deps Dependencies) *handlers.PromotionsHandler {
	if deps.Promotions == nil {
		return nil
	}
	return handlers.NewPromotionsHandler(deps.Promotions, deps.Notifications)
}

// mountBloemPromotionsViewer is called inside /api/bloem/v1 alongside the
// client surface. It takes the native seasonal viewer's authority chain, and
// serves what /api/v1/promotions and the home layout's `promoted` row do, so
// nothing a viewer sees of a campaign depends on Silo's /api/v1.
func mountBloemPromotionsViewer(r chi.Router, handler *handlers.PromotionsHandler, client bloemClientSurface, system *handlers.BloemSystemHandler) {
	available := handler != nil && client.auth != nil && client.tenant != nil && client.viewer != nil
	if system != nil {
		system.SetPromotionsViewerAvailable(available)
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
		r.Get("/promotions", handler.HandleNativeList)
		r.Post("/promotions/{id}/save", handler.HandleSave)
		r.Put("/promotions/{id}/dismissals/{surface}", handler.HandleDismiss)
	})
}
