package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// mountV2 mounts the native Vondel API independently from the Silo-compatible
// /api/v1 projection. Organization listing is account-authenticated but occurs
// before a tenant is selected; future organization-bound routes must add
// tenantMW.RequireV2.
func mountV2(r chi.Router, deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware) {
	var store handlers.V2OrganizationStore
	var membershipStore handlers.AdminContextSessionStore
	var resolver handlers.AdminContextSessionResolver
	if deps.DB != nil {
		tenants := tenancy.NewStore(deps.DB)
		store = tenants
		membershipStore = tenants
		resolver = tenancy.NewResolver(tenants)
	}
	_ = tenantMW // Administrative contexts always resolve natively, never through legacy middleware.

	tokens := deps.AdminContextTokens
	if tokens == nil && deps.Config != nil {
		tokens = auth.NewAdminContextTokenService(deps.Config.Auth.JWTSecret)
	}
	platform := deps.PlatformAdminAuthorizer
	if platform == nil && deps.DB != nil {
		platform = auth.NewPlatformAdminAuthorizer(auth.NewUserRepository(deps.DB))
	}
	var session *handlers.AdminContextSessionHandler
	var adminMW *apimw.AdminContextMiddleware
	if tokens != nil && resolver != nil && membershipStore != nil && platform != nil {
		session = handlers.NewAdminContextSessionHandler(tokens, resolver, membershipStore, platform)
		adminMW = apimw.NewAdminContextMiddleware(tokens, resolver, membershipStore, platform)
	}
	mountV2Routes(r, handlers.NewV2SystemHandler(store), session, authMW, adminMW)
}

func mountV2Routes(r chi.Router, system *handlers.V2SystemHandler, session *handlers.AdminContextSessionHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	r.Route("/api/v2", func(r chi.Router) {
		r.Get("/capabilities", system.HandleCapabilities)
		if authMW == nil {
			r.Get("/organizations", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{\"error\":\"tenant_unavailable\",\"message\":\"Tenant authorization is unavailable\"}\n"))
			})
			r.Post("/admin/session", unavailableAdminContextSession)
			mountUnavailableAdminContextRoutes(r)
			return
		}
		r.With(authMW.RequireAuth).Get("/organizations", system.HandleOrganizations)
		if session == nil {
			r.With(authMW.RequireAuth).Post("/admin/session", unavailableAdminContextSession)
		} else {
			r.With(authMW.RequireAuth).Post("/admin/session", session.HandleSession)
		}
		if adminMW == nil {
			mountUnavailableAdminContextRoutes(r)
			return
		}
		r.Route("/admin", func(r chi.Router) {
			r.Use(adminMW.Require)
			// Task 1 establishes the enforcement boundary. Later tasks mount the
			// Platform and Organization resources inside this group.
			r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("{\"error\":\"not_found\",\"message\":\"Administrative resource not found\"}\n"))
			})
		})
	})
}

func mountUnavailableAdminContextRoutes(r chi.Router) {
	r.Route("/admin", func(r chi.Router) {
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("{\"error\":\"tenant_unavailable\",\"message\":\"Tenant authorization is unavailable\"}\n"))
		})
	})
}

func unavailableAdminContextSession(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("{\"error\":\"tenant_unavailable\",\"message\":\"Tenant authorization is unavailable\"}\n"))
}
