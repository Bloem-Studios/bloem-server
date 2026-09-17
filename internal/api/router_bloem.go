package api

import (
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// mountBloem mounts the native Bloem API independently from the Silo-compatible
// /api/v1 projection. Organization listing is account-authenticated but occurs
// before a tenant is selected; future organization-bound routes must add
// tenantMW.RequireBloem.
//
// Independently is meant literally: everything mounted here is built from
// Dependencies inside this file rather than handed down from the v1 tree, so a
// native route can be added, changed or removed without touching the projection
// that upstream Silo clients depend on.
//
// searchProvider is threaded in explicitly rather than through Dependencies: the
// real *catalog.CatalogSearchService it comes from is itself built inside
// NewRouter from Dependencies plus local state (the settings store, the search
// index event repo), not received from the caller, so there is nothing for
// Dependencies to carry. May be nil, in which case Watch search answers
// unavailable rather than searching nothing.
func mountBloem(r chi.Router, deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware, searchProvider catalog.CatalogSearchProvider, accountPolicyHandler *handlers.AdminHandler, liveTVAdmin func(http.Handler) http.Handler) {
	var store handlers.BloemOrganizationStore
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
	var platformHandler *handlers.BloemAdminPlatformHandler
	var peopleHandler *handlers.BloemAdminPeopleHandler
	var organizationHandler *handlers.BloemAdminOrganizationHandler
	var explainHandler *handlers.BloemPolicyExplainHandler
	var entitlementHandler *handlers.EntitlementTemplatesHandler
	if tokens != nil && resolver != nil && membershipStore != nil && platform != nil {
		session = handlers.NewAdminContextSessionHandler(tokens, resolver, membershipStore, platform)
		adminMW = apimw.NewAdminContextMiddleware(tokens, resolver, membershipStore, platform, authMW.LoginSessions())
	}
	if tenants != nil {
		verifier := auth.NewAccountCredentialVerifier(auth.NewUserRepository(deps.DB))
		platformHandler = handlers.NewBloemAdminPlatformHandler(tenants, verifier)
		if deps.Config != nil && deps.Config.Auth.JWTSecret != "" {
			lifecycleSecret := []byte(deps.Config.Auth.JWTSecret)
			platformHandler.SetLifecycleIdempotency(
				lifecycleidempotency.NewEncryptedCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleSecret),
				lifecycleidempotency.NewRequestDigester(lifecycleSecret),
			)
		}
		organizationHandler = handlers.NewBloemAdminOrganizationHandler(
			tenants,
			access.NewGroupStore(deps.DB),
			resourcetenancy.NewStore(deps.DB),
			invitations.NewRepository(deps.DB),
		)
		explainHandler = handlers.NewBloemPolicyExplainHandler(policy.NewDecisionRepository(deps.DB))
		var entitlementSecret []byte
		if deps.Config != nil {
			entitlementSecret = []byte(deps.Config.Auth.JWTSecret)
		}
		entitlementHandler = handlers.NewEntitlementTemplatesHandler(entitlements.NewTemplateStore(deps.DB), entitlementSecret)
		if deps.Config != nil && deps.Config.Auth.JWTSecret != "" {
			lifecycleSecret := []byte(deps.Config.Auth.JWTSecret)
			entitlementHandler.SetLifecycleIdempotency(
				lifecycleidempotency.NewEncryptedCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleSecret),
				lifecycleidempotency.NewRequestDigester(lifecycleSecret),
			)
		}
	}
	peopleService := deps.AdminPeopleService
	if peopleService == nil && deps.DB != nil && deps.Config != nil {
		peopleService = adminpeople.NewService(deps.DB, deps.Config.Auth.JWTSecret)
	}
	if peopleService != nil {
		peopleHandler = handlers.NewBloemAdminPeopleHandlerWithWake(peopleService, deps.AdminPeopleWorker)
		if deps.DB != nil && deps.Config != nil && deps.Config.Auth.JWTSecret != "" {
			lifecycleSecret := []byte(deps.Config.Auth.JWTSecret)
			peopleHandler.SetLifecycleIdempotency(
				lifecycleidempotency.NewEncryptedCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleSecret),
				lifecycleidempotency.NewRequestDigester(lifecycleSecret),
			)
		}
		if deps.DB != nil {
			peopleHandler.SetCohortStore(entitlements.NewTemplateStore(deps.DB))
		}
	}
	// Compatibility Applications administration: platform-scoped lifecycle
	// state and controls for the removable compatibility applications. The
	// handler consumes the lifecycle service; it never writes application
	// tables and never touches Docker.
	var compatibilityHandler *handlers.BloemAdminCompatibilityHandler
	if deps.CompatApplications != nil {
		compatibilityHandler = handlers.NewBloemAdminCompatibilityHandler(deps.CompatApplications, deps.PublicURL)
	}
	system := handlers.NewBloemSystemHandler(store)
	if deps.DB != nil {
		system.SetLifecycleIdempotencyPhase(lifecycleidempotency.NewPostgresStore(deps.DB).CurrentPhase)
	}
	// Advertise from the condition that actually mounts /auth/profile-login:
	// the auth stack builds only with both a database and a config, and a
	// capability that disagrees with the route table is worse than no
	// capability at all.
	system.SetDirectProfileLoginAvailable(deps.DB != nil && deps.Config != nil)
	mountBloemRoutes(r, system, session, authMW, adminMW,
		bloemRouteSurfaces{
			Client:   newBloemClientSurface(deps, authMW, tenantMW, searchProvider, liveTVAdmin),
			Platform: platformHandler, People: peopleHandler, Organization: organizationHandler,
			Explain: explainHandler, Compatibility: compatibilityHandler,
			Entitlement: entitlementHandler, AccountPolicy: accountPolicyHandler,
		})
}

// NativeAPIPrefix is shared with handlers that build self-referential links.
const NativeAPIPrefix = handlers.NativeAPIPrefix

// bloemRouteSurfaces lists optional route groups; nil handlers leave their routes unmounted.
type bloemRouteSurfaces struct {
	Client        bloemClientSurface
	Platform      *handlers.BloemAdminPlatformHandler
	People        *handlers.BloemAdminPeopleHandler
	Organization  *handlers.BloemAdminOrganizationHandler
	Explain       *handlers.BloemPolicyExplainHandler
	Compatibility *handlers.BloemAdminCompatibilityHandler
	Entitlement   *handlers.EntitlementTemplatesHandler
	AccountPolicy *handlers.AdminHandler
}

// mountBloemRoutes assembles every native route inside one chi subtree.
func mountBloemRoutes(r chi.Router, system *handlers.BloemSystemHandler, session *handlers.AdminContextSessionHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware, surfaces bloemRouteSurfaces) {
	platformHandler := surfaces.Platform
	peopleHandler := surfaces.People
	organizationHandler := surfaces.Organization
	explainHandler := surfaces.Explain
	compatibilityHandler := surfaces.Compatibility
	entitlementHandler := surfaces.Entitlement
	accountPolicyHandler := surfaces.AccountPolicy
	client := surfaces.Client
	r.Route("/api/bloem/v1", func(r chi.Router) {
		r.Get("/capabilities", system.HandleCapabilities)
		client.mount(r)
		if authMW == nil {
			r.Get("/organizations", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{\"error\":\"tenant_unavailable\",\"message\":\"Tenant authorization is unavailable\"}\n"))
			})
			r.Post("/admin/session", unavailableAdminContextSession)
		} else {
			r.With(authMW.RequireAuth).Get("/organizations", system.HandleOrganizations)
			if session == nil {
				r.With(authMW.RequireAuth).Post("/admin/session", unavailableAdminContextSession)
			} else {
				r.With(authMW.RequireAuth).Post("/admin/session", session.HandleSession)
			}
		}
		if authMW == nil || adminMW == nil {
			mountUnavailableAdminContextRoutes(r)
			return
		}
		mountPlatformEntitlementScopedRoutes(r, accountPolicyHandler, authMW, adminMW)
		r.Route("/admin", func(r chi.Router) {
			r.Use(adminMW.Require)
			if entitlementHandler != nil {
				entitlement := entitlementHandler
				r.Get("/platform/entitlement-templates", entitlement.HandleList)
				r.Post("/platform/entitlement-templates", entitlement.HandleCreate)
				r.Get("/platform/entitlement-templates/{key}", entitlement.HandleGet)
				r.Get("/platform/entitlement-templates/{key}/revisions", entitlement.HandleListRevisions)
				r.Get("/platform/entitlement-templates/{key}/history", entitlement.HandleListRevisions)
				r.Post("/platform/entitlement-templates/{key}/revisions", entitlement.HandleRevise)
				r.Post("/platform/entitlement-templates/{key}/clone", entitlement.HandleClone)
				r.Post("/platform/entitlement-templates/{key}/archive", entitlement.HandleArchive)
				r.Get("/platform/organizations/{id}/entitlement", entitlement.HandleGetOrganizationEntitlement)
				r.Get("/platform/organizations/{id}/entitlement/audit", entitlement.HandleOrganizationAudit)
				r.Post("/platform/organizations/{id}/entitlement/dry-run", entitlement.HandleOrganizationDryRun)
				r.Post("/platform/organizations/{id}/entitlement/apply", entitlement.HandleOrganizationApply)
				r.Post("/platform/accounts/{account_id}/entitlement/dry-run", entitlement.HandleAccountDryRun)
				r.Post("/platform/accounts/{account_id}/entitlement/apply", entitlement.HandleAccountApply)
				r.Get("/platform/users/{user_id}/entitlement", entitlement.HandleGetAccountEntitlement)
				r.Post("/platform/users/{user_id}/entitlement/dry-run", entitlement.HandleAccountDryRun)
				r.Post("/platform/users/{user_id}/entitlement/apply", entitlement.HandleAccountApply)
			}
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
				r.Get("/organization/entitlement-cohorts", people.HandleListEntitlementCohorts)
				r.Get("/organization/entitlement-cohorts/{cohort_id}", people.HandleGetEntitlementCohort)
				r.Route("/organization/people", func(r chi.Router) {
					r.Get("/", people.HandleListPeople)
					r.Post("/selections", people.HandleCreateSelection)
					r.Post("/policy-previews", people.HandleCreatePolicyPreview)
					r.Post("/policy-jobs", people.HandleCreatePolicyJob)
					r.Get("/policy-jobs/{job_id}", people.HandleGetPolicyJob)
					r.Post("/policy-jobs/{job_id}/cancel", people.HandleCancelPolicyJob)
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

// mountPlatformEntitlementScopedRoutes is the one v2 platform surface that
// may authenticate with either a short-lived platform context or a narrowly
// scoped API key. It contains only authoritative policy reads and the bulk
// workflow. Selecting the established validator from the bearer-token prefix
// keeps both credential formats on their normal validation path.
func mountPlatformEntitlementScopedRoutes(r chi.Router, handler *handlers.AdminHandler, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if handler == nil || authMW == nil || adminMW == nil {
		return
	}
	eitherPlatformCredential := func(next http.Handler) http.Handler {
		admin := adminMW.Require(next)
		apiKey := authMW.RequireAuth(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") && strings.HasPrefix(parts[1], "sa_") {
				apiKey.ServeHTTP(w, r)
				return
			}
			admin.ServeHTTP(w, r)
		})
	}
	r.Group(func(r chi.Router) {
		r.Use(eitherPlatformCredential)
		// Registered one literal pattern at a time rather than by ranging over a
		// table. The route inventory analyses this file statically and models no
		// loop construct, so a `for range` here made the whole inventory refuse to
		// build -- and a pattern read from a struct field is not recoverable
		// statically even if it did. Both problems disappear when the pattern is a
		// literal at the call site.
		//
		// chi's Handle matches every method, so each route carries its own
		// single-method gate; methodOnly keeps that from being restated 14 times.
		r.Handle("/admin/platform/accounts/{account_id}/entitlement", methodOnly(http.MethodGet, handler.HandleGetAccountPolicy))
		r.Handle("/admin/platform/organizations/{organization_id}/accounts/{account_id}/entitlement", methodOnly(http.MethodGet, handler.HandleGetOrganizationAccountPolicy))
		r.Handle("/admin/platform/accounts/entitlement-snapshots", methodOnly(http.MethodPost, handler.HandleGetAccountPolicySnapshots))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-snapshots", methodOnly(http.MethodPost, handler.HandleGetOrganizationAccountPolicySnapshots))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-cohorts", methodOnly(http.MethodGet, handler.HandleListPlatformEntitlementCohorts))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-cohorts/{cohort_id}", methodOnly(http.MethodGet, handler.HandleGetPlatformEntitlementCohort))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews", methodOnly(http.MethodPost, handler.HandleCreatePlatformOrganizationPolicyPreview))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs", methodOnly(http.MethodPost, handler.HandleCreatePlatformOrganizationPolicyJob))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}", methodOnly(http.MethodGet, handler.HandleGetPlatformOrganizationPolicyJob))
		r.Handle("/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel", methodOnly(http.MethodPost, handler.HandleCancelPlatformOrganizationPolicyJob))
		r.Handle("/admin/platform/accounts/entitlement-bulk/policy-previews", methodOnly(http.MethodPost, handler.HandleCreatePlatformDirectPolicyPreview))
		r.Handle("/admin/platform/accounts/entitlement-bulk/policy-jobs", methodOnly(http.MethodPost, handler.HandleCreatePlatformDirectPolicyJob))
		r.Handle("/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}", methodOnly(http.MethodGet, handler.HandleGetPlatformDirectPolicyJob))
		r.Handle("/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel", methodOnly(http.MethodPost, handler.HandleCancelPlatformDirectPolicyJob))
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

// methodOnly adapts a single-method handler to chi's Handle, which registers
// every method. Anything else answers 405 with the Allow header set, which is
// what the table-driven registration this replaced did inline.
func methodOnly(method string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Allow", method)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte("{\"error\":\"method_not_allowed\",\"message\":\"Method not allowed\"}\n"))
	})
}
