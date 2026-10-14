package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/preview"
)

func bloemPreviewAdminHandler(deps Dependencies) *handlers.BloemPreviewAdminHandler {
	if deps.DB == nil {
		return nil
	}
	hosts := []string{deps.PublicURL}
	if deps.Config != nil {
		hosts = append(hosts, deps.Config.Server.PublicURL)
	}
	return handlers.NewBloemPreviewAdminHandler(preview.NewAdmin(deps.DB, bloemServerIdentity(deps), hosts...))
}

// Both formats go through their established validators. Handler and store
// checks then require live Platform authority, regardless of credential type.
func bloemEitherPlatformCredential(authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		admin := adminMW.Require(next)
		key := authMW.RequireAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fields := strings.Fields(r.Header.Get("Authorization"))
			if len(fields) == 2 && strings.EqualFold(fields[0], "bearer") && strings.HasPrefix(fields[1], "sa_") {
				key.ServeHTTP(w, r)
				return
			}
			admin.ServeHTTP(w, r)
		})
	}
}

func mountBloemPreviewAdmin(r chi.Router, h *handlers.BloemPreviewAdminHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h == nil || authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Post("/admin/platform/preview/account", h.HandleEnsureAccount)
		r.Get("/admin/platform/preview/settings", h.HandleGetSettings)
		r.Put("/admin/platform/preview/settings", h.HandlePutSettings)
		r.Put("/admin/platform/preview/libraries", h.HandlePutLibraries)
		r.Put("/admin/platform/preview/keys", h.HandlePutKeys)
		r.Delete("/admin/platform/preview/sessions", h.HandleRevokeSessions)
		r.Post("/admin/platform/preview/verify", h.HandleVerifyTicket)
		r.Get("/admin/platform/preview/capacity", h.HandleCapacity)
		r.Get("/admin/platform/preview/catalog", h.HandleCatalog)
		r.Put("/admin/platform/preview/accounts/{account_id}/presentation", h.HandlePersonalPresentation)
	})
}
