package api

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/usercollections"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/go-chi/chi/v5"
)

func newRouteInventoryRouter(t *testing.T) chi.Routes {
	t.Helper()
	pool := newV1TenancyDatabase(t)
	store := tenancy.NewStore(pool)
	bootstrap := v1TenancyBootstrap{store: store}
	// Conditional routes are the point of this fixture: a route that only
	// mounts when its dependency is present is still admitted by an allowed
	// prefix in production, so an inventory taken without it describes a
	// router nobody runs. Playback and stream need a session manager and the
	// file and folder repositories; imported collections need the user
	// collection service.
	//
	// The S3-backed subtitle routes are no longer a gap here: subtitle search,
	// download, upload, and the AI jobs are shared mutations rather than the
	// profile's own state, so they are outside the surface entirely and the
	// fixture's inability to mount them no longer hides anything.
	router := newChiRouter(Dependencies{
		DB:                    pool,
		Config:                &config.Config{Auth: config.AuthConfig{JWTSecret: "route-inventory", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		SessionMgr:            playback.NewSessionManager(4, 2),
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
		UserCollectionSync: usercollections.NewService(
			pgstore.NewPostgresProvider(pool),
			catalog.NewItemRepository(pool),
			catalog.NewLibraryItemRepository(pool),
			nil,
			nil,
			nil,
		),
	})
	return router
}

// walkRoutes returns every registered route as a "METHOD /pattern" pair.
// Collapsing to bare paths would hide the case this boundary most needs to
// see: one verb on a path being profile-scoped while another is not.
func walkRoutes(t *testing.T, routes chi.Routes) []string {
	t.Helper()
	seen := map[string]bool{}
	if err := chi.Walk(routes, func(method string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[method+" "+route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	pairs := make([]string, 0, len(seen))
	for pair := range seen {
		pairs = append(pairs, pair)
	}
	sort.Strings(pairs)
	return pairs
}

// splitRoute separates a "METHOD /pattern" pair.
func splitRoute(pair string) (string, string) {
	method, pattern, found := strings.Cut(pair, " ")
	if !found {
		return "", pair
	}
	return method, pattern
}

// The direct-profile surface is default-deny, so the risk is not a route
// nobody classified — that one is already refused — but a route that quietly
// lands inside an allowed subtree. This pins the admitted set: adding a route
// under one of the allowed prefixes fails here and forces the author to say
// out loud that a single profile may use it.
func TestDirectProfileAllowedRoutesAreThisExactSet(t *testing.T) {
	routes := newRouteInventoryRouter(t)
	var allowed []string
	for _, pair := range walkRoutes(t, routes) {
		if directProfileRouteAllowed(splitRoute(pair)) {
			allowed = append(allowed, pair)
		}
	}

	want := []string{
		"GET /api/bloem/v1/book-requests/capability",
		"GET /api/bloem/v1/book-requests/mine",
		"GET /api/bloem/v1/book-requests/{id}",
		"POST /api/bloem/v1/book-requests",
		"POST /api/bloem/v1/book-requests/{id}/cancel",
		"GET /api/bloem/v1/ebook-requests/capability",
		"GET /api/bloem/v1/ebook-requests/mine",
		"GET /api/bloem/v1/ebook-requests/{id}",
		"POST /api/bloem/v1/ebook-requests",
		"POST /api/bloem/v1/ebook-requests/{id}/cancel",
	}
	sort.Strings(want)

	if strings.Join(allowed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("direct-profile surface changed.\n got:\n%s\nwant:\n%s\n\n"+
			"A route entered or left the allowed set. Confirm a session bound to one "+
			"profile may use it, then update this list.",
			strings.Join(allowed, "\n"), strings.Join(want, "\n"))
	}
}

// The account-scoped surfaces stay outside the allowlist, including the ones
// that share a prefix with something allowed.
func TestAccountRoutesAreOutsideTheDirectProfileSurface(t *testing.T) {
	for _, pair := range []string{
		"GET /api/v1/auth/me",
		"GET /api/v1/auth/sessions",
		"DELETE /api/v1/auth/sessions/{id}",
		"POST /api/v1/auth/device/approve",
		"POST /api/v1/auth/device/deny",
		"POST /api/v1/auth/impersonation/end",
		"POST /api/v1/auth/plugin-launch",
		"POST /api/v1/api-keys/",
		"GET /api/v1/profiles/",
		"POST /api/v1/profiles/",
		"GET /api/v1/profiles/household/sessions",
		// The same path a bound profile may edit, on the verb that deletes it.
		"DELETE /api/v1/profiles/{id}",
		"GET /api/v1/settings/",
		"GET /api/v1/settings/{key}",
		"PUT /api/v1/settings/{key}",
		"GET /api/v1/compat/connect-info",
		"GET /api/v1/diagnostics/status",
		"GET /api/v1/history-imports/sources",
		"GET /api/v1/plex-sync/connections",
		"GET /api/v1/webhook-sync/connections",
		"GET /api/v1/invitations/{token}/",
		"GET /api/v1/onboarding/state",
		"GET /api/v1/requests/",
		"GET /api/v1/admin/users",
		"GET /api/v1/livetv/channels",
	} {
		if directProfileRouteAllowed(splitRoute(pair)) {
			t.Errorf("%s is inside the direct-profile surface and should not be", pair)
		}
	}
}

// Every allowlist entry has to correspond to a route that exists, or the list
// is describing a router that no longer does.
func TestDirectProfileAllowlistHasNoStaleEntries(t *testing.T) {
	routes := newRouteInventoryRouter(t)
	registered := map[string]bool{}
	for _, pair := range walkRoutes(t, routes) {
		_, pattern := splitRoute(pair)
		registered[pattern] = true
	}
	for pattern := range directProfileAllowedRoutes {
		if !registered[pattern] {
			t.Errorf("allowlist names %s, which the router does not register", pattern)
		}
	}
}
