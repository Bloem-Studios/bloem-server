package api

// Bloem wiring hooks called from newChiRouter (router.go). Each function here
// replaces a block Bloem used to carry inline in the Silo-owned router, so the
// upstream file holds one call line per concern instead of the wiring itself.
// Call order in router.go is unchanged from the inline blocks they replace.

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/compatapi"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/invitations"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
	"github.com/Silo-Server/silo-server/internal/remote"
	"github.com/Silo-Server/silo-server/internal/serverid"
	"github.com/Silo-Server/silo-server/internal/serveridentity"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bloemServerIdentity is the deployment identity in the Resolve shape Bloem's
// lifecycle and identity handlers take. It reads upstream's
// serveridentity row through the raw settings repo, exactly as the v2
// listener's ServerIdentity does: the identity is public discovery data, and a
// SECRET_KEY rotation must not change who the server is. Without a database
// every resolution answers serverid.ErrUnavailable.
func bloemServerIdentity(deps Dependencies) *serverid.Resolver {
	var store serveridentity.Store
	if deps.DB != nil {
		store = catalog.NewServerSettingsRepo(deps.DB)
	}
	return serverid.FromService(serveridentity.New(store))
}

// bloemAudienceTickets returns the audience-ticket store every websocket
// handshake shares, adopting the notifications system's store when it has one
// and otherwise installing this one on it.
func bloemAudienceTickets(deps Dependencies) auth.AudienceTicketStore {
	audienceTickets := auth.NewAudienceTicketStore(deps.RedisClient)
	if deps.Notifications != nil && deps.Notifications.AudienceTickets != nil {
		audienceTickets = deps.Notifications.AudienceTickets
	} else if deps.Notifications != nil {
		deps.Notifications.AudienceTickets = audienceTickets
	}
	return audienceTickets
}

// useBloemLifecyclePreflight installs the lifecycle-idempotency preflight in
// the base chain (after header normalization, before client-IP resolution).
func useBloemLifecyclePreflight(r chi.Router, deps Dependencies) {
	if deps.DB != nil {
		phase := lifecycleidempotency.NewPostgresStore(deps.DB).CurrentPhase
		preflight := apimw.NewLifecycleIdempotencyPreflight(phase, func(method, path string) bool {
			_, matched := MatchLifecycleRoute(method, path)
			return matched
		})
		r.Use(preflight.Handler)
	}
}

// wireBloemAuthService installs direct profile credentials and the tenancy
// ownership/membership seams on the auth service.
func wireBloemAuthService(authService *auth.Service, profileCredentials *auth.ProfileCredentialService, deps Dependencies) {
	authService.SetProfileCredentialService(profileCredentials)
	authService.SetOwnershipBootstrapper(deps.OwnershipBootstrapper)
	authService.SetMembershipProvisioner(deps.MembershipProvisioner)
}

// wireBloemViewerTokenResolver gives the viewer-access middleware a resolver
// for stream-token-authorized media-delivery requests. Such a request carries
// no bearer claims and passed through no tenant middleware, so its scope
// cannot rely on a tenant already sitting in context; the resolver resolves
// one fresh from the token's own uid/pid.
func wireBloemViewerTokenResolver(viewer *apimw.ViewerAccessMiddleware, resolver policy.AccessScopeResolver, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	tenantStore := tenancy.NewStore(pool)
	viewer.SetTokenResolver(policy.NewTenantViewerResolver(
		resolver,
		tenancy.NewSubjectResolver(tenancy.NewResolver(tenantStore), tenantStore),
	))
}

// wireBloemPlaybackSessionLimits replaces upstream's account-only session
// limit provider with the tenant-aware one and installs the session context
// provider.
func wireBloemPlaybackSessionLimits(sessions *playback.SessionManager, pool *pgxpool.Pool, users access.UserRepository, groups *access.GroupStore) {
	var sessionTenants policy.SubjectTenantResolver
	var tenantOrgStore *tenancy.Store
	if pool != nil {
		tenantStore := tenancy.NewStore(pool)
		sessionTenants = tenancy.NewSubjectResolver(tenancy.NewResolver(tenantStore), tenantStore)
		// The SAME store also answers the park tenant quota/freeze lookup
		// (bloem-park growth G2) — a different question from the OPA
		// policy subject resolution above, but the same organizations
		// table, so one Store instance answers both.
		tenantOrgStore = tenantStore
	}
	if sessionTenants != nil {
		sessions.SetContextProvider(playbackSessionContextProvider(sessionTenants))
	}
	sessions.SetLimitProvider(playbackSessionLimitProvider(users, groups, sessionTenants, tenantOrgStore))
}

func playbackSessionContextProvider(tenants policy.SubjectTenantResolver) playback.SessionContextProvider {
	return func(ctx context.Context, userID int, profileID string) (context.Context, error) {
		if tenants == nil {
			return nil, tenancy.ErrTenantUnavailable
		}
		tenant, err := tenants.ResolveSubjectTenant(ctx, userID, profileID)
		if err != nil {
			return nil, err
		}
		return tenancy.WithContext(ctx, tenant), nil
	}
}

func playbackSessionLimitProvider(
	users access.UserRepository,
	groups access.GroupPolicyProvider,
	tenants policy.SubjectTenantResolver,
	tenantOrgs *tenancy.Store,
) playback.SessionLimitProvider {
	return func(ctx context.Context, userID int, profileID string) (playback.SessionLimits, error) {
		if groups != nil {
			if tenants == nil {
				return playback.SessionLimits{}, tenancy.ErrTenantUnavailable
			}
			tenant, err := tenants.ResolveSubjectTenant(ctx, userID, profileID)
			if err != nil {
				return playback.SessionLimits{}, err
			}
			ctx = tenancy.WithContext(ctx, tenant)
		}
		user, err := users.GetByID(ctx, userID)
		if err != nil {
			return playback.SessionLimits{}, err
		}
		subject := access.GroupSubject{AccountID: user.ID, ProfileID: profileID}
		if groups != nil {
			subject, err = access.GroupSubjectFromContext(ctx, user.ID, profileID)
			if err != nil {
				return playback.SessionLimits{}, err
			}
		}
		effective, err := access.EffectivePolicyForSubject(ctx, user, subject, groups)
		if err != nil {
			return playback.SessionLimits{}, err
		}
		limits := playback.SessionLimits{
			MaxStreams:               effective.MaxStreams,
			MaxTranscodes:            effective.MaxTranscodes,
			PlaybackDisabled:         !effective.PlaybackAllowed,
			TranscodingDisabled:      !effective.TranscodeAllowed,
			AudioTranscodingDisabled: !effective.AudioTranscodeAllowed,
		}
		// Park tenant entitlements (bloem-park growth G2): the shared
		// transcode pool and the frozen flag ride the same lookup, keyed by
		// account rather than by the active-profile policy subject above —
		// a park tenant's quota is sold per account, not per profile.
		if tenantOrgs != nil {
			tenantLimits, err := tenantOrgs.TenantLimitsForUser(ctx, userID)
			if err != nil {
				return playback.SessionLimits{}, err
			}
			limits.TenantID = tenantLimits.TenantID
			limits.TenantMaxTranscodes = tenantLimits.MaxTranscodes
			limits.TenantFrozen = tenantLimits.Frozen
		}
		return limits, nil
	}
}

// wireBloemLiveTV gives the shared Live TV service its Xtream credential
// cipher and records its playback into watch history.
func wireBloemLiveTV(deps Dependencies) {
	if deps.LiveTV != nil && deps.SecretCipher != nil {
		deps.LiveTV.SetXtreamCipher(deps.SecretCipher)
	}
	if deps.LiveTV != nil {
		deps.LiveTV.SetHistoryRecorder(handlers.NewLiveTVHistoryRecorder(
			handlers.NewPGPlaybackAdminStore(deps.DB, deps.EventsHub),
		))
	}
}

// newBloemRemoteControlHandler builds admin remote control, session rail
// (S-5a, docs/specs/admin-remote-control.md). The sender rides the playback
// handler's existing socket path; the audit lives in Postgres when there is
// one. Built under the same session-manager guard as the socket itself.
func newBloemRemoteControlHandler(
	deps Dependencies,
	playbackHandler *handlers.PlaybackHandler,
	deviceHandler *handlers.DeviceHandler,
	profileHandler *handlers.ProfileHandler,
	playbackSessionsLoader *handlers.PlaybackSessionsLoader,
) *handlers.RemoteControlHandler {
	var remoteStore remote.Store = remote.NewMemoryStore()
	if deps.DB != nil {
		remoteStore = remote.NewPostgresStore(deps.DB)
	}
	var remoteLimiter ratelimit.RateLimiter
	if deps.RateLimitMW != nil {
		remoteLimiter = deps.RateLimitMW.SharedLimiter()
	}
	remoteService := remote.NewService(remoteStore, handlers.NewPlaybackRemoteSender(playbackHandler), remoteLimiter, remote.DefaultConfig())
	playbackHandler.RemoteObserver = remoteService
	if deviceHandler != nil {
		deviceHandler.RemoteCapabilities = remoteService
	}
	remoteControlHandler := handlers.NewRemoteControlHandler(remoteService, playbackHandler, profileHandler, deps.UserStoreProvider)
	if remoteControlHandler != nil && playbackSessionsLoader != nil {
		remoteControlHandler.SessionsLoader = playbackSessionsLoader
	}
	return remoteControlHandler
}

// wireBloemAdmin extends the admin handler with Bloem's tenancy, entitlement
// and host-stats wiring and builds the tenant admin handlers (bloem-park
// growth G2). Lifecycle idempotency is installed by wireBloemLifecycle.
func wireBloemAdmin(
	deps Dependencies,
	userRepo *auth.UserRepository,
	adminHandler *handlers.AdminHandler,
	profileHandler *handlers.ProfileHandler,
) (*handlers.AdminTenantsHandler, *handlers.AdminTenantMembersHandler) {
	adminHandler.SetProfileHandler(profileHandler)
	adminHandler.SetMembershipProvisioner(deps.MembershipProvisioner)
	adminHandler.HostStatsSource = deps.HostStatsSource
	if deps.OnUserProfileSessionsRevoked != nil {
		adminHandler.OnUserProfileSessionsRevoked = deps.OnUserProfileSessionsRevoked
	}
	if deps.DB == nil {
		return nil, nil
	}
	// The tenant admin API (bloem-park growth G2): a park tenant is an
	// organization, so the same tenancy.Store the OPA subject resolution uses
	// also answers this.
	tenantOrgStore := tenancy.NewStore(deps.DB)
	adminHandler.SetTenantStore(tenantOrgStore)
	entitlementStore := entitlements.NewTemplateStore(deps.DB)
	adminHandler.SetDirectEntitlements(entitlementStore)
	adminHandler.SetAccountPolicies(entitlementStore)
	platformPeople := deps.AdminPeopleService
	if platformPeople == nil && deps.Config != nil {
		platformPeople = adminpeople.NewService(deps.DB, deps.Config.Auth.JWTSecret)
	}
	platformAuthorizer := deps.PlatformAdminAuthorizer
	if platformAuthorizer == nil {
		platformAuthorizer = auth.NewPlatformAdminAuthorizer(userRepo)
	}
	adminHandler.SetPlatformEntitlementAuthorizer(platformAuthorizer)
	if platformPeople != nil {
		adminHandler.SetPlatformEntitlementBulk(entitlementStore, platformPeople, tenantOrgStore, platformAuthorizer, deps.AdminPeopleWorker)
	}
	adminTenantsHandler := handlers.NewAdminTenantsHandler(tenantOrgStore, userRepo)
	memberAccounts := auth.NewAccountProvisioner(userRepo, deps.UserStoreProvider)
	memberService := tenancy.NewMemberService(
		deps.DB,
		memberAccounts,
		userRepo,
		auth.NewSessionRepository(deps.DB),
	)
	memberService.SetCompatSessionInvalidator(deps.OnUserSessionsRevoked)
	adminTenantMembersHandler := handlers.NewAdminTenantMembersHandler(memberService, adminHandler)
	memberService.SetResourcePurger(adminTenantMembersHandler)
	return adminTenantsHandler, adminTenantMembersHandler
}

// bloemLifecycleTargets names every handler that takes lifecycle
// idempotency. Nil members are skipped.
type bloemLifecycleTargets struct {
	auth          *handlers.AuthHandler
	requests      *handlers.RequestsHandler
	profiles      *handlers.ProfileHandler
	settingValues *handlers.SettingValuesHandler
	admin         *handlers.AdminHandler
	tenants       *handlers.AdminTenantsHandler
	members       *handlers.AdminTenantMembersHandler
}

// wireBloemLifecycle installs lifecycle idempotency on every legacy handler
// that supports it. It runs once, after every target is constructed and
// before any request is served; the setters only store their arguments, so
// installing them here is equivalent to installing them at construction.
//
// The auth handler is wired whenever the database and config exist (an empty
// secret included); every other handler requires a non-empty JWT secret.
// Profiles use the plaintext HMAC-keyed coordinator, the rest the encrypted one.
func wireBloemLifecycle(deps Dependencies, t bloemLifecycleTargets) {
	if deps.DB == nil || deps.Config == nil {
		return
	}
	lifecycleSecret := []byte(deps.Config.Auth.JWTSecret)
	encrypted := func() lifecycleidempotency.Coordinator {
		return lifecycleidempotency.NewEncryptedCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleSecret)
	}
	if t.auth != nil {
		t.auth.SetLifecycleIdempotency(
			encrypted(),
			lifecycleidempotency.NewRequestDigester(lifecycleSecret),
			lifecycleidempotency.NewPreauthActorDigester(lifecycleSecret),
			bloemServerIdentity(deps),
		)
	}
	if deps.Config.Auth.JWTSecret == "" {
		return
	}
	if t.requests != nil {
		t.requests.SetLifecycleIdempotency(encrypted(), lifecycleidempotency.NewRequestDigester(lifecycleSecret))
	}
	if t.profiles != nil {
		t.profiles.SetLifecycleIdempotency(
			lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleidempotency.NewHMACKeyDigester(lifecycleSecret)),
			lifecycleidempotency.NewRequestDigester(lifecycleSecret),
		)
	}
	if t.settingValues != nil {
		t.settingValues.SetLifecycleIdempotency(encrypted(), lifecycleidempotency.NewRequestDigester(lifecycleSecret))
	}
	if t.admin != nil {
		t.admin.SetLifecycleIdempotency(encrypted(), lifecycleidempotency.NewRequestDigester(lifecycleSecret))
	}
	if t.tenants != nil {
		t.tenants.SetLifecycleIdempotency(encrypted(), lifecycleidempotency.NewRequestDigester(lifecycleSecret))
	}
	if t.members != nil {
		t.members.SetLifecycleIdempotency(encrypted(), lifecycleidempotency.NewRequestDigester(lifecycleSecret))
	}
}

// newBloemV1InvitationHandler builds the /api/v1 invitation handler. It is a
// separate instance from the one v2 consumes: only the legacy surface carries
// lifecycle idempotency.
func newBloemV1InvitationHandler(deps Dependencies, invitationService *invitations.Service, accessGroupStore *access.GroupStore) *handlers.InvitationHandler {
	invitationHandler := handlers.NewInvitationHandler(invitationService)
	if deps.Config.Auth.JWTSecret != "" {
		lifecycleSecret := []byte(deps.Config.Auth.JWTSecret)
		invitationHandler.SetLifecycleIdempotency(
			lifecycleidempotency.NewCoordinator(lifecycleidempotency.NewPostgresStore(deps.DB), lifecycleidempotency.NewHMACKeyDigester(lifecycleSecret)),
			lifecycleidempotency.NewRequestDigester(lifecycleSecret),
			bloemServerIdentity(deps),
			lifecycleSecret,
		)
	}
	if accessGroupStore != nil {
		invitationHandler.SetAccessGroupProvider(accessGroupStore)
	}
	return invitationHandler
}

// newBloemTenantMiddleware builds the tenant middleware the legacy /api/v1
// chains and the Bloem surfaces use (nil without a database).
func newBloemTenantMiddleware(deps Dependencies) *apimw.TenantMiddleware {
	if deps.DB == nil {
		return nil
	}
	return apimw.NewTenantMiddleware(tenancy.NewResolver(tenancy.NewStore(deps.DB)))
}

// wireBloemRouter runs Bloem's router-level wiring at the point Silo's router
// has built every legacy handler: lifecycle idempotency, the Bloem-native
// /api/bloem/v1 surface and the private compatibility API.
func wireBloemRouter(
	r chi.Router,
	deps Dependencies,
	tenantMiddleware *apimw.TenantMiddleware,
	lifecycle bloemLifecycleTargets,
	authMiddleware *apimw.AuthMiddleware,
	searchProvider catalog.CatalogSearchProvider,
	requireActingAdmin func(http.Handler) http.Handler,
) {
	wireBloemLifecycle(deps, lifecycle)
	mountBloem(r, deps, authMiddleware, tenantMiddleware, searchProvider, lifecycle.admin, requireActingAdmin)
	mountBloemCompatAPI(r, deps)
}

// wireBloemV2 hands the v2 listener Bloem's native tenant identity and the
// stream-token authorizer its media routes accept.
func wireBloemV2(v2deps *apiv2.Dependencies, deps Dependencies, tenantMiddleware *apimw.TenantMiddleware, authMiddleware *apimw.AuthMiddleware) {
	if tenantMiddleware != nil {
		v2deps.TenantIdentity = tenantMiddleware.ResolveNative
	}
	if deps.Config != nil {
		v2deps.StreamTokens = authMiddleware.StreamTokenAuth(deps.Config.Auth.JWTSecret)
	}
}

// mountBloemCompatAPI mounts the private Compatibility Service API v1
// (internal/compatapi). This is an internal surface for enrolled compatibility
// applications only — it is not part of the public API: the edge gateway must
// never forward /api/internal/** from public ingress, and every operation
// additionally authenticates the calling application's service credential.
// Mounted as its own separated route group; nil fails closed with no routes.
func mountBloemCompatAPI(r chi.Router, deps Dependencies) {
	if deps.CompatAPIV1 != nil {
		r.Route("/api/internal/compat/v1", func(r chi.Router) {
			r.Use(compatapi.RelativePaths)
			deps.CompatAPIV1.RegisterRoutes(r)
		})
	}
}

// newBloemEngagementHandlers builds Bloem's /api/v1 engagement handlers.
// Active deployment-wide ambience packs ride on the public branding payload
// so effects work on the login screen (S-3). The ambience registry and the S-2
// promotion CRUD are registered in the admin group by
// mountBloemLegacyAdminV1. Each handler exists only when its service is wired
// (the route goldens wire neither).
func newBloemEngagementHandlers(deps Dependencies, brandingHandler *handlers.BrandingHandler) (*handlers.AmbienceHandler, *handlers.AdminPromotionsHandler) {
	if brandingHandler != nil && deps.Ambience != nil {
		brandingHandler.SetAmbience(deps.Ambience)
	}
	var ambienceHandler *handlers.AmbienceHandler
	if deps.Ambience != nil {
		ambienceHandler = handlers.NewAmbienceHandler(deps.Ambience)
	}
	var adminPromotionsHandler *handlers.AdminPromotionsHandler
	if deps.Promotions != nil {
		adminPromotionsHandler = handlers.NewAdminPromotionsHandler(deps.Promotions)
	}
	return ambienceHandler, adminPromotionsHandler
}

// mountBloemPublicV1 mounts public (pre-login) ambience artwork serving.
// ambienceHandler is non-nil exactly when deps.Ambience is.
func mountBloemPublicV1(r chi.Router, deps Dependencies, ambienceHandler *handlers.AmbienceHandler) {
	if deps.Ambience != nil {
		r.Get("/ambience/assets/{ref}", ambienceHandler.HandleServeAsset)
	}
}

// wireBloemNotifications hands the notifications handler the engagement
// services it reports on.
func wireBloemNotifications(notificationsHandler *handlers.NotificationsHandler, deps Dependencies) {
	if deps.Ambience != nil {
		notificationsHandler.SetAmbience(deps.Ambience)
	}
	notificationsHandler.SetPromotions(deps.Promotions != nil)
}

// mountBloemPromotionsV1 mounts S-2 detail / pre-playback delivery
// (docs/specs/client-engagement.md section B.3) in the authenticated group.
func mountBloemPromotionsV1(r chi.Router, deps Dependencies) {
	if deps.Promotions != nil {
		promotionHandler := handlers.NewPromotionsHandler(deps.Promotions, deps.Notifications)
		r.With(apimw.RequireProfile).Get("/promotions", promotionHandler.HandleList)
		r.With(apimw.RequireProfile).Post("/promotions/{id}/save", promotionHandler.HandleSave)
	}
}

// mountBloemLegacyAdminV1 registers every Bloem route in the legacy
// /api/v1/admin acting-admin group: tenants and members (bloem-park growth
// G2), S-1 announcements, the S-3 ambience registry, S-2 promotion CRUD, and
// the S-5a remote-control rail. Each block mounts only when its handler or
// service is wired.
func mountBloemLegacyAdminV1(
	r chi.Router,
	deps Dependencies,
	adminTenantsHandler *handlers.AdminTenantsHandler,
	adminTenantMembersHandler *handlers.AdminTenantMembersHandler,
	ambienceHandler *handlers.AmbienceHandler,
	adminPromotionsHandler *handlers.AdminPromotionsHandler,
	remoteControlHandler *handlers.RemoteControlHandler,
) {
	if adminTenantsHandler != nil {
		// Tenants (bloem-park growth G2): the
		// contract park's media adapter speaks.
		r.Post("/tenants", adminTenantsHandler.HandleCreate)
		r.Get("/tenants", adminTenantsHandler.HandleList)
		r.Get("/tenants/{id}", adminTenantsHandler.HandleGet)
		r.Patch("/tenants/{id}/limits", adminTenantsHandler.HandleUpdateLimits)
		r.Post("/tenants/{id}/freeze", adminTenantsHandler.HandleFreeze)
		r.Post("/tenants/{id}/thaw", adminTenantsHandler.HandleThaw)
		r.Delete("/tenants/{id}", adminTenantsHandler.HandleDelete)
	}
	if adminTenantMembersHandler != nil {
		r.Get("/tenants/{tenant_id}/members", adminTenantMembersHandler.HandleList)
		r.Post("/tenants/{tenant_id}/members", adminTenantMembersHandler.HandleCreate)
		r.Get("/tenants/{tenant_id}/members/{user_id}", adminTenantMembersHandler.HandleGet)
		r.Put("/tenants/{tenant_id}/members/{user_id}", adminTenantMembersHandler.HandleUpdate)
		r.Delete("/tenants/{tenant_id}/members/{user_id}", adminTenantMembersHandler.HandleDelete)
		r.Post("/tenants/{tenant_id}/members/{user_id}/suspend", adminTenantMembersHandler.HandleSuspend)
		r.Post("/tenants/{tenant_id}/members/{user_id}/resume", adminTenantMembersHandler.HandleResume)
		r.Post("/tenants/{tenant_id}/members/{user_id}/reset-password", adminTenantMembersHandler.HandleResetPassword)
		r.Get("/tenants/{tenant_id}/members/{user_id}/profiles", adminTenantMembersHandler.HandleListProfiles)
		r.Post("/tenants/{tenant_id}/members/{user_id}/profiles", adminTenantMembersHandler.HandleCreateProfile)
		r.Put("/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", adminTenantMembersHandler.HandleUpdateProfile)
		r.Delete("/tenants/{tenant_id}/members/{user_id}/profiles/{profile_id}", adminTenantMembersHandler.HandleDeleteProfile)
		r.Get("/tenants/{tenant_id}/members/{user_id}/devices", adminTenantMembersHandler.HandleListDevices)
		r.Delete("/tenants/{tenant_id}/members/{user_id}/devices/{device_id}", adminTenantMembersHandler.HandleDeleteDevice)
		r.Get("/tenants/{tenant_id}/members/{user_id}/auth-sessions", adminTenantMembersHandler.HandleListAuthSessions)
		r.Delete("/tenants/{tenant_id}/members/{user_id}/auth-sessions/{session_id}", adminTenantMembersHandler.HandleRevokeAuthSession)
		r.Delete("/tenants/{tenant_id}/members/{user_id}/auth-sessions", adminTenantMembersHandler.HandleRevokeAllAuthSessions)
	}
	if deps.Notifications != nil {
		// S-1 admin compose (docs/specs/client-engagement.md §A.5).
		announcementsHandler := handlers.NewAdminAnnouncementsHandler(deps.Notifications)
		r.Route("/notifications/announcements", func(r chi.Router) {
			r.Get("/", announcementsHandler.HandleList)
			r.Post("/", announcementsHandler.HandleCreate)
			r.Delete("/{id}", announcementsHandler.HandleDelete)
		})
	}
	if ambienceHandler != nil {
		// S-3 seasonal pack registry (docs/specs/client-engagement.md section C).
		r.Route("/ambience", func(r chi.Router) {
			r.Get("/", ambienceHandler.HandleList)
			r.Post("/", ambienceHandler.HandleCreate)
			r.Put("/{id}", ambienceHandler.HandleUpdate)
			r.Delete("/{id}", ambienceHandler.HandleDelete)
			// Standalone upload (the authoring side pushes
			// artwork before any pack exists); must be
			// registered before the {id} pattern.
			r.Post("/assets", ambienceHandler.HandleUploadAsset)
			r.Post("/{id}/assets", ambienceHandler.HandleAttachAsset)
		})
	}
	if adminPromotionsHandler != nil {
		// S-2 promotion CRUD (docs/specs/client-engagement.md section B.1).
		r.Route("/promotions", func(r chi.Router) {
			r.Get("/", adminPromotionsHandler.HandleList)
			r.Post("/", adminPromotionsHandler.HandleCreate)
			r.Put("/{id}", adminPromotionsHandler.HandleUpdate)
			r.Delete("/{id}", adminPromotionsHandler.HandleDelete)
		})
	}
	if remoteControlHandler != nil {
		r.Route("/remote", func(r chi.Router) {
			r.Get("/sessions", remoteControlHandler.HandleListSessions)
			r.Post("/sessions/{session_id}/commands", remoteControlHandler.HandleAdminSendSessionCommand)
			r.Get("/commands/{command_id}", remoteControlHandler.HandleGetCommand)
			r.Get("/audit", remoteControlHandler.HandleListAudit)
		})
	}
}

// optionalLegacyTenant resolves the legacy tenant for a request, except for
// stream-token-authorized media delivery, which resolves its own.
func optionalLegacyTenant(tenant *apimw.TenantMiddleware) func(http.Handler) http.Handler {
	if tenant == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	resolved := tenant.ResolveLegacy
	return func(next http.Handler) http.Handler {
		withTenant := resolved(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apimw.IsStreamTokenAuthorized(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			withTenant.ServeHTTP(w, r)
		})
	}
}
