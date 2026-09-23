package api

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/music"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
	"github.com/Silo-Server/silo-server/internal/recommendations"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/serverid"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
)

// bloemClientSurface is the native client surface: what a television or a phone
// talks to, as opposed to the administrative and tenancy routes the rest of
// /api/bloem/v1 serves.
//
// It exists as its own type because these routes answer to a different
// authority than the admin routes do. The admin tree requires a tenant-selected
// session (apimw.AdminContextMiddleware, and tenantMW.RequireBloem for
// organization-bound routes). A viewer's session is not tenant-selected — no
// login endpoint mints organization, membership or revision claims — so these
// routes take the same legacy tenant projection the v1 tree uses, and derive
// their media scope from the viewer's own profile instead.
type bloemClientSurface struct {
	// identity answers the public probe. It is separate from the capability
	// document because it can legitimately be unavailable; see
	// handlers.ServerIdentityHandler.
	identity *handlers.ServerIdentityHandler
	watch    *handlers.WatchHandler
	progress *handlers.ProgressHandler
	persons  *handlers.PersonDetailHandler
	music    *handlers.MusicHandler
	liveTV   *handlers.LiveTVHandler

	// itemCollections answers "which collections hold this title" for a
	// detail page's "Part of a collection" row.
	itemCollections *handlers.ItemCollectionsHandler

	// notifications serves the inbox whole. Silo's /api/v2 projection has no
	// place for Bloem's alert fields, so a client reading the v2 inbox can list
	// an alert it cannot render; this surface serves the same rows with those
	// fields on them. See handlers/bloem_notifications.go.
	notifications *handlers.NotificationsHandler
	// liveTVAdmin gates the tuner and guide-source routes inside the Live TV
	// subtree. It is the same middleware the v1 mount passes; held here only so
	// the native mount can hand it to the shared mount function.
	liveTVAdmin func(http.Handler) http.Handler

	auth      *apimw.AuthMiddleware
	tenant    *apimw.TenantMiddleware
	viewer    *apimw.ViewerAccessMiddleware
	rateLimit *ratelimit.Middleware
}

// newBloemClientSurface assembles the client surface from Dependencies. Every
// member is optional: a missing dependency leaves its routes unmounted rather
// than mounting a route that answers with an empty library.
func newBloemClientSurface(deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware, searchProvider catalog.CatalogSearchProvider, liveTVAdmin func(http.Handler) http.Handler) bloemClientSurface {
	surface := bloemClientSurface{auth: authMW, tenant: tenantMW, rateLimit: deps.RateLimitMW, liveTVAdmin: liveTVAdmin}

	// The same encrypting decorator the rest of the server reads settings
	// through: server.instance_id is a plain row, but reading it through a
	// second, undecorated store would be a second spelling of "the settings".
	var settings *catalog.EncryptedSettingsRepo
	var identityStore serverid.Store
	var setup handlers.SetupStateReporter
	var users *auth.UserRepository
	if deps.DB != nil {
		settings = catalog.NewEncryptedSettingsRepo(catalog.NewServerSettingsRepo(deps.DB), deps.SecretCipher)
		identityStore = settings
		users = auth.NewUserRepository(deps.DB)
		setup = auth.NewSetupState(users)
	}
	// The identity route is mounted even with no database behind it. A client
	// that reaches a 404 concludes the server does not have the endpoint at
	// all; the handler's 503 says "this server has it and cannot answer right
	// now", which is the truth and is what the client can retry. A nil branding
	// service is allowed and falls back to the default server name.
	surface.identity = handlers.NewServerIdentityHandler(
		serverid.NewResolver(identityStore),
		deps.BrandingService,
		setup,
	)

	if deps.DB == nil || deps.UserStoreProvider == nil {
		return surface
	}
	surface.music = handlers.NewNativeMusicHandler(music.NewPostgresRepository(deps.DB))

	// The Watch documents both TV clients consume. They need a catalog, a
	// media-file repository and a progress store; without any one of them there
	// is no document to compose. Search reuses the exact same reader — see
	// CatalogWatchReader.Search — so a nil searchProvider only narrows what one
	// method on it can do, never whether Watch mounts at all.
	liveService := deps.LiveTV
	if liveService == nil {
		liveService = livetv.NewService(deps.DB)
	}
	if deps.SecretCipher != nil {
		liveService.SetXtreamCipher(deps.SecretCipher)
	}
	var liveTVSecret string
	if deps.Config != nil {
		liveTVSecret = deps.Config.Auth.JWTSecret
	}
	surface.liveTV = handlers.NewBloemLiveTVHandler(liveService, liveTVSecret)
	if surface.liveTV != nil {
		surface.liveTV.PrimaryProfileChecker = bloemLiveTVPrimaryProfileChecker(deps.UserStoreProvider)
	}

	if deps.FileRepo != nil {
		// deps.PersonRepo is a concrete *catalog.PersonRepository, and it may be
		// nil (people features disabled). Assigning a nil pointer straight into
		// an interface-typed constructor parameter would produce a non-nil
		// interface holding a nil receiver — Credits' own "people == nil" guard
		// would then miss it, and the first call would panic dereferencing it.
		var people handlers.WatchPeopleSource
		if deps.PersonRepo != nil {
			people = deps.PersonRepo
		}
		watchReader := handlers.NewCatalogWatchReader(
			catalog.NewBrowseRepository(deps.DB),
			catalog.NewItemRepository(deps.DB),
			catalog.NewEpisodeRepository(deps.DB),
			catalog.NewSeasonRepository(deps.DB),
			deps.FileRepo,
			deps.FileRepo,
			people,
			deps.UserStoreProvider,
			deps.ImageResolver,
			searchProvider,
		)
		surface.watch = handlers.NewWatchHandler(watchReader, watchReader)
	}

	// The person-detail document needs only the people repository; it does not
	// share Watch's FileRepo dependency, so it mounts independently of it.
	if deps.PersonRepo != nil {
		surface.persons = handlers.NewPersonDetailHandler(
			deps.PersonRepo,
			catalog.NewBrowseRepository(deps.DB),
			deps.PersonRepo,
			deps.ImageResolver,
		)
	}

	// Needs only the database: the collection store and the item access check
	// are both plain repositories over deps.DB.
	surface.itemCollections = handlers.NewItemCollectionsHandler(
		catalog.NewLibraryCollectionRepository(deps.DB),
		catalog.NewItemRepository(deps.DB),
		deps.ArtworkResolver,
	)

	// A deployment with notifications off leaves these routes unmounted rather
	// than mounting an inbox that answers empty.
	if deps.Notifications != nil {
		surface.notifications = handlers.NewNotificationsHandler(deps.Notifications, deps.EventsHub)
	}

	progress := handlers.NewProgressHandler(deps.UserStoreProvider)
	progress.EventsHub = deps.EventsHub
	progress.SettingsRepo = settings
	progress.LibraryLookup = catalog.NewLibraryItemRepository(deps.DB)
	// Recommendation staleness is driven by watching, so a v2 sync marks the
	// taste profile stale exactly as a v1 sync does. A nil worker leaves the
	// refresh unqueued, which is what a deployment with recommendations off
	// wants.
	progress.SetProfileStaler(recommendations.NewRepo(deps.DB))
	progress.SetProfileRefreshRequester(deps.RecWorker)
	surface.progress = progress

	if deps.Config != nil {
		profileTokens := access.NewProfileTokenService(deps.Config.Auth.JWTSecret, 0)
		groups := access.NewGroupStore(deps.DB)
		var resolver apimw.ViewerResolver
		if deps.PolicySystem != nil {
			resolver = policy.NewViewerResolver(
				users,
				deps.UserStoreProvider,
				profileTokens,
				deps.PolicySystem.PDP(),
				resourcetenancy.NewStore(deps.DB),
				groups,
			)
		} else {
			// Legacy resolver: proxy/test wiring without a policy system.
			// Production integrated/api modes always take the policy path.
			resolver = access.NewResolver(users, deps.UserStoreProvider, profileTokens, groups)
		}
		surface.viewer = apimw.NewViewerAccessMiddleware(resolver)
		tenants := tenancy.NewStore(deps.DB)
		surface.viewer.SetTokenResolver(policy.NewTenantViewerResolver(
			resolver, tenancy.NewSubjectResolver(tenancy.NewResolver(tenants), tenants),
		))
	}

	return surface
}

// mount registers the client routes inside an /api/bloem/v1 subrouter.
//
// The identity probe is public: a client has to answer "which server is this,
// and can I log into it yet" before it holds any credentials. Everything else
// sits in one authenticated, viewer-scoped, profile-scoped group, because every
// document it serves is one profile's view of the library — the items that
// profile may watch and that profile's own progress.
func (s bloemClientSurface) mount(r chi.Router) {
	if s.identity != nil {
		r.Get("/server/identity", s.identity.HandleGetServerIdentity)
	}
	if s.auth == nil || (s.watch == nil && s.progress == nil && s.persons == nil && s.itemCollections == nil && s.music == nil && s.liveTV == nil && s.notifications == nil) {
		return
	}

	r.Group(func(r chi.Router) {
		if s.liveTV != nil {
			r.Use(bloemLiveTVStreamTokens(s.auth, s.liveTV.JWTSecret))
		}
		r.Use(s.auth.RequireAuth)
		// The same default-organization projection the v1 tree applies. These
		// routes deliberately do NOT use tenantMW.RequireBloem: it demands a
		// tenant-selected session carrying organization, membership and
		// revision claims, and no login endpoint mints those, so requiring it
		// would 401 every viewer. Organization-bound routes still must.
		r.Use(optionalLegacyTenant(s.tenant))
		if s.rateLimit != nil {
			r.Use(s.rateLimit.Handler)
		}
		if s.viewer != nil {
			// Resolves the library restrictions, content-rating ceiling and
			// playback-quality ceiling the documents are filtered by. Without
			// it the handlers see an empty scope and answer for the account.
			r.Use(s.viewer.RequireViewerAccess)
		}
		// The demo guard is intentionally absent: its blocklist is written in
		// /api/v1 path prefixes and explicitly permits playback progress, so it
		// would be a no-op here.
		r.Use(requireBloemLiveTVProfile)

		if s.notifications != nil {
			// The inbox, paged and synced the way v2 pages everything else.
			// The rows are the difference: they carry the alert fields Silo's
			// v2 projection drops, which is what makes a system.alert row
			// renderable rather than merely listable.
			// Mounted flat rather than through r.Route: chi renders a
			// subrouter's "/" as a trailing slash, and the document -- which is
			// what a client generator reads -- names the collection without
			// one.
			r.Get("/notifications", s.notifications.HandleBloemNotificationList)
			r.Get("/notifications/sync", s.notifications.HandleBloemNotificationSync)
			r.Get("/notifications/{id}", s.notifications.HandleBloemNotificationGet)
		}
		if s.liveTV != nil {
			r.Get("/livetv/capability", s.liveTV.HandleCapability)
			// Live TV is Bloem's own feature -- Silo has no livetv package and
			// lists it as a non-goal -- so it will never appear on /api/v2 and
			// the native surface is its permanent home.
			//
			// mountLiveTVRoutes is the single definition of the operational
			// surface. It is mounted here, not on the retired v1 prefix.
			// Its admin group retains its own requireActingAdmin fence.
			//
			// Coverage note: newBloemClientSurface returns early unless both
			// deps.DB and deps.UserStoreProvider are set, and liveTV is
			// assigned after that guard. The route-manifest fixture supplies a
			// DB but no UserStoreProvider, so none of these routes -- nor the
			// capability probe above -- appear in testdata/media_routes.txt.
			// They are covered by TestBloemClientSurfaceMountsLiveTV instead,
			// which builds the surface directly.
			// mountLiveTVRoutes hands its admin subtree straight to
			// chi's r.Use, which panics on a nil middleware. Production always
			// supplies one, but a surface built without it must not take the
			// whole server down -- and must not fall back to a pass-through,
			// which would expose tuner and guide-source management unguarded.
			// Deny instead: viewer routes keep working, admin ones answer 503.
			liveTVAdmin := s.liveTVAdmin
			if liveTVAdmin == nil {
				liveTVAdmin = denyLiveTVAdmin
			}
			mountLiveTVRoutes(r, s.liveTV, liveTVAdmin)
		}
		if s.watch != nil {
			r.Route("/watch", func(r chi.Router) {
				r.Get("/home", s.watch.HandleWatchHome)
				r.Get("/items/{content_id}", s.watch.HandleWatchItem)
				r.Get("/search", s.watch.HandleWatchSearch)
			})
		}
		if s.progress != nil {
			r.Post("/sync/progress", s.progress.HandleBloemSyncProgress)
		}
		if s.persons != nil {
			r.Get("/persons/{person_id}", s.persons.HandleGetPersonDetail)
		}
		if s.itemCollections != nil {
			r.Get("/catalog/items/{content_id}/collections", s.itemCollections.HandleListItemCollections)
		}
		if s.music != nil {
			r.Get("/music/status", s.music.HandleStatus)
			r.Get("/music/artists", s.music.HandleArtists)
			r.Get("/music/artists/{id}", s.music.HandleArtist)
			r.Get("/music/albums/{id}", s.music.HandleAlbum)
		}
	})
}

// denyLiveTVAdmin stands in when no acting-admin middleware was supplied. It
// refuses rather than passes through: the routes it guards add tuners and
// rewrite guide sources, so failing open would be worse than failing closed.
func denyLiveTVAdmin(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("{\"error\":\"admin_unavailable\",\"message\":\"Live TV administration is unavailable\"}\n"))
	})
}
