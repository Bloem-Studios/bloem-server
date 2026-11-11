package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

func bloemSearchMirrorHandler(deps Dependencies) *handlers.BloemSearchMirrorHandler {
	if deps.DB == nil {
		return nil
	}
	return handlers.NewBloemSearchMirrorHandler(deps.DB)
}

// mountBloemSearchMirror serves the search mirror's read-only status to a Platform
// context or an administrator's unscoped API key.
func mountBloemSearchMirror(r chi.Router, h *handlers.BloemSearchMirrorHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h == nil || authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Get("/admin/search/mirror", h.HandleGet)
	})
}
