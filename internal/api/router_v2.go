package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/activitylog"
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
	var platformHandler *handlers.V2AdminPlatformHandler
	if tokens != nil && resolver != nil && membershipStore != nil && platform != nil {
		session = handlers.NewAdminContextSessionHandler(tokens, resolver, membershipStore, platform)
		adminMW = apimw.NewAdminContextMiddleware(tokens, resolver, membershipStore, platform)
	}
	if tenants, ok := store.(*tenancy.Store); ok {
		audit := deps.ActivityLogRepo
		if audit == nil && deps.DB != nil {
			audit = activitylog.NewRepo(deps.DB)
		}
		platformHandler = handlers.NewV2AdminPlatformHandler(tenants, audit)
	}
	mountV2Routes(r, handlers.NewV2SystemHandler(store), session, authMW, adminMW, platformHandler)
}

func mountV2Routes(r chi.Router, system *handlers.V2SystemHandler, session *handlers.AdminContextSessionHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware, platformHandlers ...*handlers.V2AdminPlatformHandler) {
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
			if len(platformHandlers) > 0 && platformHandlers[0] != nil {
				platform := platformHandlers[0]
				r.Route("/platform/organizations", func(r chi.Router) {
					r.Get("/", platform.HandleListOrganizations)
					r.Post("/", platform.HandleCreateOrganization)
					r.Route("/{id}", func(r chi.Router) {
						r.Get("/", platform.HandleGetOrganization)
						r.Patch("/", platform.HandleUpdateOrganization)
						r.Post("/suspend", platform.HandleSuspendOrganization)
						r.Post("/reactivate", platform.HandleReactivateOrganization)
						r.Post("/transfer-ownership", platform.HandleTransferOwnership)
						r.Get("/memberships", platform.HandleListMemberships)
						r.Post("/memberships", platform.HandleCreateMembership)
						r.Patch("/memberships/{membership_id}", platform.HandleUpdateMembership)
					})
				})
			}
			r.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("{\"error\":\"not_found\",\"message\":\"Administrative resource not found\"}\n"))
			}))
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
