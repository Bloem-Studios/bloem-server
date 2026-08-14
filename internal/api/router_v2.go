package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
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
	var tenants *tenancy.Store
	if deps.DB != nil {
		tenants = tenancy.NewStore(deps.DB)
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
	var peopleHandler *handlers.V2AdminPeopleHandler
	var organizationHandler *handlers.V2AdminOrganizationHandler
	var explainHandler *handlers.V2PolicyExplainHandler
	if tokens != nil && resolver != nil && membershipStore != nil && platform != nil {
		session = handlers.NewAdminContextSessionHandler(tokens, resolver, membershipStore, platform)
		adminMW = apimw.NewAdminContextMiddleware(tokens, resolver, membershipStore, platform)
	}
	if tenants != nil {
		verifier := auth.NewAccountCredentialVerifier(auth.NewUserRepository(deps.DB))
		platformHandler = handlers.NewV2AdminPlatformHandler(tenants, verifier)
		organizationHandler = handlers.NewV2AdminOrganizationHandler(
			tenants,
			access.NewGroupStore(deps.DB),
			resourcetenancy.NewStore(deps.DB),
			invitations.NewRepository(deps.DB),
		)
		explainHandler = handlers.NewV2PolicyExplainHandler(policy.NewDecisionRepository(deps.DB))
	}
	peopleService := deps.AdminPeopleService
	if peopleService == nil && deps.DB != nil && deps.Config != nil {
		peopleService = adminpeople.NewService(deps.DB, deps.Config.Auth.JWTSecret)
	}
	if peopleService != nil {
		peopleHandler = handlers.NewV2AdminPeopleHandlerWithWake(peopleService, deps.AdminPeopleWorker)
	}
	// Compatibility Applications administration: platform-scoped lifecycle
	// state and controls for the removable compatibility applications. The
	// handler consumes the lifecycle service; it never writes application
	// tables and never touches Docker.
	var compatibilityHandler *handlers.V2AdminCompatibilityHandler
	if deps.CompatApplications != nil {
		compatibilityHandler = handlers.NewV2AdminCompatibilityHandler(deps.CompatApplications, deps.PublicURL)
	}
	system := handlers.NewV2SystemHandler(store)
	// Advertise from the condition that actually mounts /auth/profile-login:
	// the auth stack builds only with both a database and a config, and a
	// capability that disagrees with the route table is worse than no
	// capability at all.
	system.SetDirectProfileLoginAvailable(deps.DB != nil && deps.Config != nil)
	mountV2Routes(r, system, session, authMW, adminMW, platformHandler, peopleHandler, organizationHandler, explainHandler, compatibilityHandler)
}

func mountV2Routes(r chi.Router, system *handlers.V2SystemHandler, session *handlers.AdminContextSessionHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware, adminHandlers ...any) {
	var platformHandler *handlers.V2AdminPlatformHandler
	var peopleHandler *handlers.V2AdminPeopleHandler
	var organizationHandler *handlers.V2AdminOrganizationHandler
	var explainHandler *handlers.V2PolicyExplainHandler
	var compatibilityHandler *handlers.V2AdminCompatibilityHandler
	for _, candidate := range adminHandlers {
		switch handler := candidate.(type) {
		case *handlers.V2AdminPlatformHandler:
			platformHandler = handler
		case *handlers.V2AdminPeopleHandler:
			peopleHandler = handler
		case *handlers.V2AdminOrganizationHandler:
			organizationHandler = handler
		case *handlers.V2PolicyExplainHandler:
			explainHandler = handler
		case *handlers.V2AdminCompatibilityHandler:
			compatibilityHandler = handler
		}
	}
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
			if organizationHandler != nil {
				organization := organizationHandler
				r.Route("/organization", func(r chi.Router) {
					r.Get("/overview", organization.HandleOverview)
					r.Route("/groups", func(r chi.Router) {
						r.Get("/", organization.HandleListGroups)
						r.Post("/", organization.HandleCreateGroup)
						r.Get("/{id}", organization.HandleGetGroup)
						r.Put("/{id}", organization.HandleUpdateGroup)
						r.Delete("/{id}", organization.HandleDeleteGroup)
					})
					r.Get("/libraries", organization.HandleListLibraries)
					r.Put("/entitlements/{folder_id}", organization.HandleUpdateEntitlement)
					r.Delete("/entitlements/{folder_id}", organization.HandleDeleteEntitlement)
					r.Get("/invitations", organization.HandleListInvitations)
					r.Post("/invitations", organization.HandleCreateInvitation)
				})
			}
			if explainHandler != nil {
				explain := explainHandler
				r.Get("/organization/policy-decisions", explain.HandleListDecisions)
				r.Get("/organization/policy-decisions/{id}", explain.HandleGetDecision)
			}
			if platformHandler != nil {
				platform := platformHandler
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
			if compatibilityHandler != nil {
				compatibility := compatibilityHandler
				r.Route("/platform/compatibility", func(r chi.Router) {
					r.Get("/applications", compatibility.HandleListApplications)
					r.Post("/enrollments", compatibility.HandleCreateEnrollment)
					r.Route("/applications/{instance_id}", func(r chi.Router) {
						r.Post("/enable", compatibility.HandleEnableApplication)
						r.Post("/disable", compatibility.HandleDisableApplication)
						r.Post("/rotate-credential", compatibility.HandleRotateCredential)
						r.Post("/revoke", compatibility.HandleRevokeApplication)
					})
				})
			}
			if peopleHandler != nil {
				people := peopleHandler
				r.Route("/organization/people", func(r chi.Router) {
					r.Get("/", people.HandleListPeople)
					r.Post("/selections", people.HandleCreateSelection)
					r.Post("/bulk-jobs", people.HandleCreateBulkJob)
					r.Get("/bulk-jobs/{job_id}", people.HandleGetBulkJob)
					r.Route("/{account_id}", func(r chi.Router) {
						r.Get("/", people.HandleGetPerson)
						r.Patch("/memberships/current", people.HandleUpdateMembership)
						r.Patch("/profiles/{profile_id}", people.HandleUpdateProfile)
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
