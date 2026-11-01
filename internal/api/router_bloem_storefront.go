package api

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemPlatformStorefrontHandlers are the platform-administration handlers a
// storefront drives with its API key. Each nil handler leaves its routes
// unmounted.
type bloemPlatformStorefrontHandlers struct {
	Tenants       *handlers.AdminTenantsHandler
	TenantMembers *handlers.AdminTenantMembersHandler
	Accounts      *handlers.AdminHandler
	Announcements *handlers.AdminAnnouncementsHandler
	Promotions    *handlers.AdminPromotionsHandler
	Ambience      *handlers.AmbienceHandler
	// ServeAmbienceAssets mounts the public artwork route; it needs the
	// ambience registry itself, not only a handler that answers 503.
	ServeAmbienceAssets bool
	DB                  *pgxpool.Pool
}

// newBloemPlatformStorefrontHandlers builds them from the same pieces as the
// legacy /api/v1/admin routes, so the native routes behave alike.
func newBloemPlatformStorefrontHandlers(deps Dependencies, admin *handlers.AdminHandler, promotions *handlers.AdminPromotionsHandler, ambience *handlers.AmbienceHandler) bloemPlatformStorefrontHandlers {
	out := bloemPlatformStorefrontHandlers{Accounts: admin, Promotions: promotions, Ambience: ambience,
		ServeAmbienceAssets: deps.Ambience != nil, DB: deps.DB}
	if deps.Notifications != nil {
		out.Announcements = handlers.NewAdminAnnouncementsHandler(deps.Notifications)
	}
	if deps.DB != nil && admin != nil {
		out.Tenants, out.TenantMembers = newBloemTenantAdminHandlers(deps, auth.NewUserRepository(deps.DB), admin, tenancy.NewStore(deps.DB))
	}
	return out
}

// mountBloemPlatformStorefront serves, under /api/bloem/v1/admin/platform, the
// platform administration that a storefront (Bloem Garden) needs: reseller
// organizations (tenants) and their members, creating and updating an account
// on a plan, an account's profiles, devices and sign-in sessions,
// announcements, campaigns and seasonal packs. Each
// route takes a Platform administrative context or an enabled administrator's
// unscoped API key. The handlers are the ones behind the legacy /api/v1/admin
// routes, which stay mounted for the clients that still call them.
//
// It also serves uploaded seasonal artwork publicly at /ambience/assets/{ref}.
func mountBloemPlatformStorefront(r chi.Router, h bloemPlatformStorefrontHandlers, authMW *apimw.AuthMiddleware, adminMW *apimw.AdminContextMiddleware) {
	if h.Ambience != nil && h.ServeAmbienceAssets {
		r.Get("/ambience/assets/{ref}", h.Ambience.HandleServeAsset)
	}
	if authMW == nil || adminMW == nil {
		return
	}
	r.Group(func(r chi.Router) {
		// Named as the route inventory recognizes it (internal/routeinventory).
		eitherPlatformCredential := bloemEitherPlatformCredential(authMW, adminMW)
		r.Use(eitherPlatformCredential)
		r.Use(handlers.RequireBloemPlatformAdmin(h.DB))

		if tenants := h.Tenants; tenants != nil {
			r.Get("/admin/platform/tenants", tenants.HandleList)
			r.Post("/admin/platform/tenants", tenants.HandleCreate)
			r.Get("/admin/platform/tenants/{id}", tenants.HandleGet)
			r.Delete("/admin/platform/tenants/{id}", tenants.HandleDelete)
			r.Patch("/admin/platform/tenants/{id}/limits", tenants.HandleUpdateLimits)
			r.Post("/admin/platform/tenants/{id}/freeze", tenants.HandleFreeze)
			r.Post("/admin/platform/tenants/{id}/thaw", tenants.HandleThaw)
		}
		if members := h.TenantMembers; members != nil {
			r.Get("/admin/platform/tenants/{tenant_id}/members", members.HandleList)
			r.Post("/admin/platform/tenants/{tenant_id}/members", members.HandleCreate)
			r.Get("/admin/platform/tenants/{tenant_id}/members/{user_id}", members.HandleGet)
			r.Put("/admin/platform/tenants/{tenant_id}/members/{user_id}", members.HandleUpdate)
			r.Delete("/admin/platform/tenants/{tenant_id}/members/{user_id}", members.HandleDelete)
			r.Post("/admin/platform/tenants/{tenant_id}/members/{user_id}/suspend", members.HandleSuspend)
			r.Post("/admin/platform/tenants/{tenant_id}/members/{user_id}/resume", members.HandleResume)
			r.Post("/admin/platform/tenants/{tenant_id}/members/{user_id}/reset-password", members.HandleResetPassword)
			r.Get("/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles", members.HandleListProfiles)
			r.Post("/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles", members.HandleCreateProfile)
			r.Put("/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", members.HandleUpdateProfile)
			r.Delete("/admin/platform/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", members.HandleDeleteProfile)
			r.Get("/admin/platform/tenants/{tenant_id}/members/{user_id}/devices", members.HandleListDevices)
			r.Delete("/admin/platform/tenants/{tenant_id}/members/{user_id}/devices/{device_id}", members.HandleDeleteDevice)
			r.Get("/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions", members.HandleListAuthSessions)
			r.Delete("/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}", members.HandleRevokeAuthSession)
			r.Delete("/admin/platform/tenants/{tenant_id}/members/{user_id}/auth-sessions", members.HandleRevokeAllAuthSessions)
		}
		if accounts := h.Accounts; accounts != nil {
			// Create and update on a plan, on top of Silo's account service
			// (the one behind /api/v2/admin/users, which takes no plan).
			r.Post("/admin/platform/accounts", accounts.HandleBloemPlatformAccountCreate)
			r.Put("/admin/platform/accounts/{account_id}", accounts.HandleBloemPlatformAccountUpdate)
			r.Get("/admin/platform/accounts/{account_id}/profiles", handlers.BloemPlatformAccountResource(accounts.HandleListUserProfiles))
			r.Post("/admin/platform/accounts/{account_id}/profiles", handlers.BloemPlatformAccountResource(accounts.HandleCreateUserProfile))
			r.Put("/admin/platform/accounts/{account_id}/profiles/{profile_id}", handlers.BloemPlatformAccountResource(accounts.HandleUpdateUserProfile))
			r.Delete("/admin/platform/accounts/{account_id}/profiles/{profile_id}", handlers.BloemPlatformAccountResource(accounts.HandleDeleteUserProfile))
			r.Get("/admin/platform/accounts/{account_id}/devices", handlers.BloemPlatformAccountResource(accounts.HandleListUserDevices))
			r.Delete("/admin/platform/accounts/{account_id}/devices/{device_id}", handlers.BloemPlatformAccountResource(accounts.HandleDeleteUserDevice))
			r.Get("/admin/platform/accounts/{account_id}/auth-sessions", handlers.BloemPlatformAccountResource(accounts.HandleListUserAuthSessions))
			r.Delete("/admin/platform/accounts/{account_id}/auth-sessions/{session_id}", handlers.BloemPlatformAccountResource(accounts.HandleRevokeUserAuthSession))
			r.Delete("/admin/platform/accounts/{account_id}/auth-sessions", handlers.BloemPlatformAccountResource(accounts.HandleRevokeAllUserAuthSessions))
		}
		if announcements := h.Announcements; announcements != nil {
			r.Get("/admin/platform/announcements", announcements.HandleList)
			r.Post("/admin/platform/announcements", announcements.HandleCreate)
			r.Delete("/admin/platform/announcements/{id}", announcements.HandleDelete)
		}
		if promotion := h.Promotions; promotion != nil {
			r.Route("/admin/platform/promotions", func(r chi.Router) {
				r.Get("/", promotion.HandleList)
				r.Post("/", promotion.HandleCreate)
				r.Put("/{id}", promotion.HandleUpdate)
				r.Delete("/{id}", promotion.HandleDelete)
			})
		}
		if ambience := h.Ambience; ambience != nil {
			r.Route("/admin/platform/ambience", func(r chi.Router) {
				r.Get("/", ambience.HandleList)
				r.Post("/", ambience.HandleCreate)
				r.Put("/{id}", ambience.HandleUpdate)
				r.Delete("/{id}", ambience.HandleDelete)
				// Before the {id} pattern, as in the legacy registry.
				r.Post("/assets", ambience.HandleUploadAsset)
				r.Post("/{id}/assets", ambience.HandleAttachAsset)
			})
		}
	})
}
