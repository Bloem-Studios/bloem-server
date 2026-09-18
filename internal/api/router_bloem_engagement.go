package api

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/go-chi/chi/v5"
)

// Reuse the existing registries, with native administrative-context authority.
// The legacy Silo-compatible projection is unchanged.
func mountBloemEngagementRoutes(r chi.Router, promotion *handlers.AdminPromotionsHandler, ambience *handlers.AmbienceHandler) {
	r.Group(func(r chi.Router) {
		r.Use(handlers.RequireBloemPlatformContext)
		if promotion != nil {
			r.Route("/platform/promotions", func(r chi.Router) {
				r.Get("/", promotion.HandleList)
				r.Post("/", promotion.HandleCreate)
				r.Put("/{id}", promotion.HandleUpdate)
				r.Delete("/{id}", promotion.HandleDelete)
			})
		}
		if ambience != nil {
			r.Route("/platform/ambience", func(r chi.Router) {
				r.Get("/", ambience.HandleList)
				r.Post("/", ambience.HandleCreate)
				r.Put("/{id}", ambience.HandleUpdate)
				r.Delete("/{id}", ambience.HandleDelete)
				r.Post("/assets", ambience.HandleUploadAsset)
				r.Post("/{id}/assets", ambience.HandleAttachAsset)
			})
		}
	})
}
