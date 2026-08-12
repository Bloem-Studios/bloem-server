package api

import (
	"net/http"

	"github.com/Vondel-Media/vondel-server/internal/api/handlers"
	apimw "github.com/Vondel-Media/vondel-server/internal/api/middleware"
	"github.com/Vondel-Media/vondel-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// mountV10 mounts the native Vondel API independently from the Silo-compatible
// /api/v1 projection. Organization listing is account-authenticated but occurs
// before a tenant is selected; future organization-bound routes must add
// tenantMW.RequireV10.
func mountV10(r chi.Router, deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware) {
	var store handlers.V10OrganizationStore
	if deps.DB != nil {
		store = tenancy.NewStore(deps.DB)
	}
	mountV10Routes(r, handlers.NewV10SystemHandler(store), authMW, tenantMW)
}

func mountV10Routes(r chi.Router, system *handlers.V10SystemHandler, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware) {
	_ = tenantMW // Reserved for organization-bound v10 routes.
	r.Route("/api/v10", func(r chi.Router) {
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
