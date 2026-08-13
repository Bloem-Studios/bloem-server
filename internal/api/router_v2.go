package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// mountV2 mounts the native Vondel API independently from the Silo-compatible
// /api/v1 projection. Organization listing is account-authenticated but occurs
// before a tenant is selected; future organization-bound routes must add
// tenantMW.RequireV2.
func mountV2(r chi.Router, deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware) {
	var store handlers.V2OrganizationStore
	if deps.DB != nil {
		store = tenancy.NewStore(deps.DB)
	}
	mountV2Routes(r, handlers.NewV2SystemHandler(store), authMW, tenantMW)
}

func mountV2Routes(r chi.Router, system *handlers.V2SystemHandler, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware) {
	_ = tenantMW // Reserved for organization-bound v2 routes.
	r.Route("/api/v2", func(r chi.Router) {
		r.Get("/capabilities", system.HandleCapabilities)
		if authMW == nil {
			r.Get("/organizations", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{\"error\":\"tenant_unavailable\",\"message\":\"Tenant authorization is unavailable\"}\n"))
			})
			return
		}
		r.With(authMW.RequireAuth).Get("/organizations", system.HandleOrganizations)
	})
}
