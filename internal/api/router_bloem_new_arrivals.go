package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

func bloemNewArrivalsHandler(deps Dependencies) *handlers.BloemNewArrivalsHandler {
	if deps.DB == nil {
		return nil
	}
	return handlers.NewBloemNewArrivalsHandler(deps.DB)
}

// mountBloemNewArrivals serves recent additions to a Platform context or an
// administrator's unscoped API key, which is how a storefront writes its
// "new this week" email.
func mountBloemNewArrivals(r chi.Router, h *handlers.BloemNewArrivalsHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h == nil || authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Get("/admin/platform/new-arrivals", h.HandleGet)
	})
}
