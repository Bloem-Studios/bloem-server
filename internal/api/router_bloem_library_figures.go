package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

func bloemLibraryFiguresHandler(deps Dependencies) *handlers.BloemLibraryFiguresHandler {
	if deps.DB == nil {
		return nil
	}
	return handlers.NewBloemLibraryFiguresHandler(deps.DB)
}

// mountBloemLibraryFigures serves per-library item counts to a Platform
// context or an administrator's unscoped API key, which is how a storefront
// keeps the figures on its selling page current.
func mountBloemLibraryFigures(r chi.Router, h *handlers.BloemLibraryFiguresHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h == nil || authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Get("/admin/platform/library-figures", h.HandleGet)
	})
}
