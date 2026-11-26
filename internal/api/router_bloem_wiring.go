package api

// Bloem wiring hooks called from newChiRouter (router.go). Each function here
// replaces a block Bloem used to carry inline in the Silo-owned router, so the
// upstream file holds one call line per concern instead of the wiring itself.
// Call order in router.go is unchanged from the inline blocks they replace.

import (
	"context"
	"fmt"
	"net/http"

	mediarequests "github.com/Silo-Server/silo-server/internal/requests"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/preview"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/serverid"
	"github.com/Silo-Server/silo-server/internal/serveridentity"
	"github.com/Silo-Server/silo-server/internal/serversettings"
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
		store = serversettings.NewServerSettingsRepo(deps.DB)
	}
	return serverid.FromService(serveridentity.New(store))
}

// wireBloemAuthService installs the tenancy ownership seam and preview guests
// on the auth service.
func wireBloemAuthService(authService *auth.Service, deps Dependencies) {
	authService.SetOwnershipBootstrapper(deps.OwnershipBootstrapper)
	if reader := bloemPreviewReader(deps); reader != nil {
		authService.EnablePreviewGuests(deps.DB, reader)
	}
}

// withBloemViewerTenancy applies Bloem tenancy to the policy viewer resolver
// that Silo's line in newChiRouter builds (policy.ViewerResolver.WithBloemTenancy).
// It panics on any other resolver, so the router never starts without it.
func withBloemViewerTenancy(resolver apimw.ViewerResolver, pool *pgxpool.Pool) apimw.ViewerResolver {
	viewer, ok := resolver.(*policy.ViewerResolver)
	if !ok {
		panic(fmt.Sprintf("api: Bloem tenancy needs a *policy.ViewerResolver, got %T", resolver))
	}
	return viewer.WithBloemTenancy(resourcetenancy.NewStore(pool))
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
	viewer.SetTokenResolver(bloemPreviewTokenViewer{inner: policy.NewTenantViewerResolver(
		resolver,
		tenancy.NewSubjectResolver(tenancy.NewResolver(tenantStore), tenantStore),
	), pool: pool})
}

// wireBloemPlaybackSessionLimits replaces upstream's account-only session
// limit provider with the tenant-aware one and installs the session context
// provider.
func wireBloemPlaybackSessionLimits(sessions *playback.SessionManager, pool *pgxpool.Pool, users access.UserRepository, groups *access.TenantGroupStore) {
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
	var householdPools householdPoolResolver
	if pool != nil {
		householdPools = databaseHouseholdPools{pool: pool}
	}
	sessions.SetProfileLimitProvider(withHouseholdPoolLimits(playbackSessionLimitProvider(users, groups, sessionTenants, tenantOrgStore), householdPools))
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
) playback.ProfileSessionLimitProvider {
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
		// Bloem free play: every guest shares the preview account, whose
		// protected policy forbids playback. A request guest admission marked
		// for a live grant may play and transcode; the account-wide stream
		// caps would make one guest's play block another's, so the preview's
		// free-play capacity limits concurrent grants instead.
		if _, ok := previewcontext.FreePlayGrant(ctx); ok {
			limits = playback.SessionLimits{}
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

// wireBloemAdmin extends the admin handler with Bloem's tenancy, entitlement
// and host-stats wiring and builds the tenant admin handlers (bloem-park
// growth G2). Lifecycle idempotency is installed by wireBloemLifecycle.
func wireBloemAdmin(
	deps Dependencies,
	userRepo *auth.UserRepository,
	adminHandler *handlers.AdminHandler,
	profileHandler *handlers.ProfileHandler,
) {
	adminHandler.SetProfileHandler(profileHandler)
	adminHandler.BloemOnUserSessionsRevoked = deps.BloemOnUserSessionsRevoked
	adminHandler.HostStatsSource = deps.HostStatsSource
	if deps.OnUserProfileSessionsRevoked != nil {
		adminHandler.OnUserProfileSessionsRevoked = deps.OnUserProfileSessionsRevoked
	}
	if deps.DB == nil {
		return
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
}

// newBloemTenantAdminHandlers builds the tenant and tenant-member handlers
// over one member service for the native /api/bloem/v2/admin/platform/tenants
// routes.
func newBloemTenantAdminHandlers(
	deps Dependencies,
	userRepo *auth.UserRepository,
	adminHandler *handlers.AdminHandler,
	tenantOrgStore *tenancy.Store,
) (*handlers.AdminTenantsHandler, *handlers.AdminTenantMembersHandler) {
	memberAccounts := auth.NewAccountProvisioner(userRepo, deps.UserStoreProvider)
	memberService := tenancy.NewMemberService(
		deps.DB,
		memberAccounts,
		userRepo,
		auth.NewSessionRepository(deps.DB),
	)
	memberService.SetCompatSessionInvalidator(deps.BloemOnUserSessionsRevoked)
	memberService.SetPasswordPolicy(adminHandler.BloemCheckNewPassword)
	adminTenantMembersHandler := handlers.NewAdminTenantMembersHandler(memberService, adminHandler)
	memberService.SetResourcePurger(adminTenantMembersHandler)
	adminTenantsHandler := handlers.NewAdminTenantsHandler(tenantOrgStore, memberService)
	return adminTenantsHandler, adminTenantMembersHandler
}

// newBloemTenantMiddleware builds the tenant middleware the Bloem surfaces
// use (nil without a database).
func newBloemTenantMiddleware(deps Dependencies) *apimw.TenantMiddleware {
	if deps.DB == nil {
		return nil
	}
	return apimw.NewTenantMiddleware(tenancy.NewResolver(tenancy.NewStore(deps.DB)))
}

// wireBloemRouter runs Bloem's router-level wiring at the point Silo's router
// has built every legacy handler: the Bloem-native /api/bloem/v2 surface and
// the private compatibility API.
func wireBloemRouter(
	r chi.Router,
	deps Dependencies,
	tenantMiddleware *apimw.TenantMiddleware,
	admin *handlers.AdminHandler,
	authMiddleware *apimw.AuthMiddleware,
	searchProvider catalog.CatalogSearchProvider,
	requireActingAdmin func(http.Handler) http.Handler,
) {
	mountBloem(r, deps, authMiddleware, tenantMiddleware, searchProvider, admin, requireActingAdmin)
}

// wireBloemRequestPolicy binds the request service Silo hands the v2 listener
// to Bloem's tenant group limits and, with authentication present, the profile
// request policy. Silo assigns its bare *requests.Service to each request
// field (#5052), so this runs after those assignments and gives every field
// that holds it the bounded service. Authentication and its user repository
// are absent from a DB-only discovery router, where request routes are
// unreachable; every authenticated composition still requires the policy.
func wireBloemRequestPolicy(v2deps *apiv2.Dependencies, authenticated bool) {
	service, ok := v2deps.AdminRequests.(*mediarequests.Service)
	if !ok {
		return
	}
	bounded, err := mediarequests.NewTenantGroupLimitService(service)
	if err != nil {
		panic(fmt.Errorf("compose Bloem request group limits: %w", err))
	}
	if authenticated {
		if err := bounded.EnableTenantRequestPolicy(); err != nil {
			panic(fmt.Errorf("compose Bloem request policy: %w", err))
		}
	}
	v2deps.AdminRequests = bounded
	if current, ok := v2deps.Requests.(*mediarequests.Service); ok && current == service {
		v2deps.Requests = bounded
	}
	if current, ok := v2deps.RequestLifecycle.(*mediarequests.Service); ok && current == service {
		v2deps.RequestLifecycle = bounded
	}
	if current, ok := v2deps.WatchlistRequests.(*mediarequests.Service); ok && current == service {
		v2deps.WatchlistRequests = bounded
	}
}

// wireBloemV2 hands the v2 listener Bloem's native tenant identity and the
// stream-token authorizer its media routes accept.
func wireBloemV2(v2deps *apiv2.Dependencies, deps Dependencies, tenantMiddleware *apimw.TenantMiddleware, authMiddleware *apimw.AuthMiddleware) {
	if deps.DB != nil {
		wireBloemRequestPolicy(v2deps, authMiddleware != nil)
		wireBloemAdminRequestUsage(v2deps)
	}
	if tenantMiddleware != nil {
		v2deps.TenantIdentity = tenantMiddleware.ResolveNative
	}
	if deps.Config != nil {
		freePlay := newBloemFreePlayDelivery(deps)
		v2deps.StreamTokens = bloemStreamTokenAuth(authMiddleware, deps.Config.Auth.JWTSecret, freePlay)
		if freePlay != nil {
			v2deps.PreviewFreePlay = freePlay
		}
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

// bloemPreviewHandler builds the public preview endpoints. Nil without a
// database or a signing secret.
func bloemPreviewHandler(deps Dependencies) *handlers.BloemPreviewHandler {
	reader := bloemPreviewReader(deps)
	if reader == nil || deps.Config == nil || deps.Config.Auth.JWTSecret == "" {
		return nil
	}
	jwt := auth.NewJWTService(deps.Config.Auth.JWTSecret, deps.Config.Auth.AccessTokenExpiry, deps.Config.Auth.RefreshTokenExpiry)
	guests := auth.NewPreviewGuests(deps.DB, jwt, reader)
	buckets := preview.NewClientBucketsWithSecret(deps.Config.Auth.JWTSecret)
	handler := handlers.NewBloemPreviewHandler(preview.NewExchanger(deps.DB, guests, bloemServerIdentity(deps), buckets), reader, buckets)
	handler.SetEvents(preview.NewEvents(deps.DB))
	return handler
}

// bloemThemeSongs gives the theme songs handler Silo's router built Bloem's
// content viewer and content policy (handlers.BloemThemeSongs). Silo builds a
// *handlers.ThemeSongsHandler; anything else means a Silo change moved that
// construction, and serving themes without Bloem's policy would be wrong, so
// it panics at startup instead.
func bloemThemeSongs(service apiv2.ThemeSongService, viewer apimw.ViewerResolver) apiv2.ThemeSongService {
	songs, ok := service.(*handlers.ThemeSongsHandler)
	if !ok {
		panic(fmt.Sprintf("api: theme songs are %T, not *handlers.ThemeSongsHandler; Bloem's content policy cannot wrap them", service))
	}
	return handlers.BloemThemeSongs(songs, viewer)
}

// bloemPluginContent puts plugin pages and assets behind Bloem's content
// boundary. Silo builds a *plugins.ContentHandler; anything else means a Silo
// change moved that construction, and serving plugin content without the
// boundary would be wrong, so it panics at startup instead.
func bloemPluginContent(service apiv2.PluginContentService, boundary bloemPluginBoundary) apiv2.PluginContentService {
	content, ok := service.(*plugins.ContentHandler)
	if !ok {
		panic(fmt.Sprintf("api: plugin content is %T, not *plugins.ContentHandler; Bloem's content boundary cannot wrap it", service))
	}
	content.WrapAccess(boundary.content)
	return content
}
