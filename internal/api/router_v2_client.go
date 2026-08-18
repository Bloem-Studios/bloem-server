package api

import (
	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/policy"
	"github.com/Silo-Server/silo-server/internal/ratelimit"
	"github.com/Silo-Server/silo-server/internal/recommendations"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/serverid"
	"github.com/go-chi/chi/v5"
)

// v2ClientSurface is the native client surface: what a television or a phone
// talks to, as opposed to the administrative and tenancy routes the rest of
// /api/v2 serves.
//
// It exists as its own type because these routes answer to a different
// authority than the admin routes do. The admin tree requires a tenant-selected
// session (apimw.AdminContextMiddleware, and tenantMW.RequireV2 for
// organization-bound routes). A viewer's session is not tenant-selected — no
// login endpoint mints organization, membership or revision claims — so these
// routes take the same legacy tenant projection the v1 tree uses, and derive
// their media scope from the viewer's own profile instead.
type v2ClientSurface struct {
	// identity answers the public probe. It is separate from the capability
	// document because it can legitimately be unavailable; see
	// handlers.ServerIdentityHandler.
	identity *handlers.ServerIdentityHandler
	watch    *handlers.WatchHandler
	progress *handlers.ProgressHandler
	persons  *handlers.PersonDetailHandler

	auth      *apimw.AuthMiddleware
	tenant    *apimw.TenantMiddleware
	viewer    *apimw.ViewerAccessMiddleware
	rateLimit *ratelimit.Middleware
}

// newV2ClientSurface assembles the client surface from Dependencies. Every
// member is optional: a missing dependency leaves its routes unmounted rather
// than mounting a route that answers with an empty library.
func newV2ClientSurface(deps Dependencies, authMW *apimw.AuthMiddleware, tenantMW *apimw.TenantMiddleware, searchProvider catalog.CatalogSearchProvider) v2ClientSurface {
	surface := v2ClientSurface{auth: authMW, tenant: tenantMW, rateLimit: deps.RateLimitMW}

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

	// The Watch documents both TV clients consume. They need a catalog, a
	// media-file repository and a progress store; without any one of them there
	// is no document to compose. Search reuses the exact same reader — see
	// CatalogWatchReader.Search — so a nil searchProvider only narrows what one
	// method on it can do, never whether Watch mounts at all.
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
	}

	return surface
}

// mount registers the client routes inside an /api/v2 subrouter.
//
// The identity probe is public: a client has to answer "which server is this,
// and can I log into it yet" before it holds any credentials. Everything else
// sits in one authenticated, viewer-scoped, profile-scoped group, because every
// document it serves is one profile's view of the library — the items that
// profile may watch and that profile's own progress.
func (s v2ClientSurface) mount(r chi.Router) {
	if s.identity != nil {
		r.Get("/server/identity", s.identity.HandleGetServerIdentity)
	}
	if s.auth == nil || (s.watch == nil && s.progress == nil && s.persons == nil) {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(s.auth.RequireAuth)
		// The same default-organization projection the v1 tree applies. These
		// routes deliberately do NOT use tenantMW.RequireV2: it demands a
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
		r.Use(apimw.RequireProfile)

		if s.watch != nil {
			r.Route("/watch", func(r chi.Router) {
				r.Get("/home", s.watch.HandleWatchHome)
				r.Get("/items/{content_id}", s.watch.HandleWatchItem)
				r.Get("/search", s.watch.HandleWatchSearch)
			})
		}
		if s.progress != nil {
			r.Post("/sync/progress", s.progress.HandleV2SyncProgress)
		}
		if s.persons != nil {
			r.Get("/persons/{person_id}", s.persons.HandleGetPersonDetail)
		}
	})
}
