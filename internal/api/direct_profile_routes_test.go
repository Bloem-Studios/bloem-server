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
	// Known gap, deliberately recorded rather than hidden: the subtitle
	// search, download, upload, and AI routes mount only with an S3 client,
	// which this fixture has no way to supply. They sit under the allowed
	// "/api/v1/subtitles" prefix and are therefore admitted in production
	// without appearing in the pinned set below.
	router := NewRouter(Dependencies{
		DB:                    pool,
		Config:                &config.Config{Auth: config.AuthConfig{JWTSecret: "route-inventory", AccessTokenExpiry: time.Hour, RefreshTokenExpiry: time.Hour}},
		UserStoreProvider:     pgstore.NewPostgresProvider(pool),
		OwnershipBootstrapper: bootstrap,
		MembershipProvisioner: bootstrap,
		SessionMgr:            playback.NewSessionManager(4, 2),
		FileRepo:              scanner.NewFileRepository(pool),
		FolderRepo:            catalog.NewFolderRepository(pool),
		UserCollectionSync: usercollections.NewService(
			pgstore.NewPostgresProvider(pool),
			catalog.NewItemRepository(pool),
			catalog.NewLibraryItemRepository(pool),
			nil,
			nil,
		),
	})
	routes, ok := router.(chi.Routes)
	if !ok {
		t.Fatal("router does not expose its route table")
	}
	return routes
}

func walkRoutePatterns(t *testing.T, routes chi.Routes) []string {
	t.Helper()
	seen := map[string]bool{}
	if err := chi.Walk(routes, func(_ string, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		seen[route] = true
		return nil
	}); err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	patterns := make([]string, 0, len(seen))
	for pattern := range seen {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	return patterns
}

// The direct-profile surface is default-deny, so the risk is not a route
// nobody classified — that one is already refused — but a route that quietly
// lands inside an allowed subtree. This pins the admitted set: adding a route
// under one of the allowed prefixes fails here and forces the author to say
// out loud that a single profile may use it.
func TestDirectProfileAllowedRoutesAreThisExactSet(t *testing.T) {
	routes := newRouteInventoryRouter(t)
	var allowed []string
	for _, pattern := range walkRoutePatterns(t, routes) {
		if directProfileRouteAllowed(pattern) {
			allowed = append(allowed, pattern)
		}
	}

	want := []string{
		"/api/v1/audio-prefs/{series_id}",
		"/api/v1/auth/logout",
		"/api/v1/calendar",
		"/api/v1/catalog",
		"/api/v1/catalog/audiobook-groups",
		"/api/v1/catalog/filters",
		"/api/v1/catalog/filters/search",
		"/api/v1/catalog/items/{id}",
		"/api/v1/catalog/items/{id}/episodes",
		"/api/v1/catalog/items/{id}/manga-files",
		"/api/v1/catalog/items/{id}/versions",
		"/api/v1/catalog/query",
		"/api/v1/catalog/series/{id}/seasons",
		"/api/v1/catalog/series/{id}/seasons/{num}",
		"/api/v1/catalog/series/{id}/seasons/{num}/episodes",
		"/api/v1/collections/",
		"/api/v1/collections/capabilities",
		"/api/v1/collections/groups",
		"/api/v1/collections/groups/order",
		"/api/v1/collections/groups/{id}",
		"/api/v1/collections/import/mdblist",
		"/api/v1/collections/import/mdblist/search",
		"/api/v1/collections/import/mdblist/top",
		"/api/v1/collections/import/tmdb",
		"/api/v1/collections/import/trakt",
		"/api/v1/collections/order",
		"/api/v1/collections/preview",
		"/api/v1/collections/server",
		"/api/v1/collections/templates",
		"/api/v1/collections/{id}",
		"/api/v1/collections/{id}/image",
		"/api/v1/collections/{id}/items",
		"/api/v1/collections/{id}/items/order",
		"/api/v1/collections/{id}/items/{item_id}",
		"/api/v1/collections/{id}/sync",
		"/api/v1/devices/",
		"/api/v1/devices/{device_id}",
		"/api/v1/devices/{device_id}/settings",
		"/api/v1/direct-download",
		"/api/v1/downloads/",
		"/api/v1/downloads/batches/{batch_id}/manifests",
		"/api/v1/downloads/capability",
		"/api/v1/downloads/subscriptions",
		"/api/v1/downloads/subscriptions/sync",
		"/api/v1/downloads/subscriptions/{id}",
		"/api/v1/downloads/{id}",
		"/api/v1/downloads/{id}/artwork/{kind}",
		"/api/v1/downloads/{id}/file",
		"/api/v1/downloads/{id}/file-proxy",
		"/api/v1/downloads/{id}/manifest",
		"/api/v1/downloads/{id}/subtitles/{ref}",
		"/api/v1/favorites/",
		"/api/v1/favorites/{item_id}",
		"/api/v1/health",
		"/api/v1/history/",
		"/api/v1/history/remove",
		"/api/v1/home/dismissals/{surface}/{item_id}",
		"/api/v1/home/layout",
		"/api/v1/home/sections",
		"/api/v1/home/sections/{id}/items",
		"/api/v1/items/trailers/capability",
		"/api/v1/items/{id}/translate-description",
		"/api/v1/library-playback-prefs/",
		"/api/v1/library-playback-prefs/{library_id}",
		"/api/v1/library/{id}/collections",
		"/api/v1/library/{id}/collections/{collection_id}/items",
		"/api/v1/library/{id}/layout",
		"/api/v1/library/{id}/sections",
		"/api/v1/library/{id}/sections/{sectionId}/items",
		"/api/v1/library/{id}/user-collections",
		"/api/v1/metadata/ai/status",
		"/api/v1/playback/capability",
		"/api/v1/playback/route-events",
		"/api/v1/playback/sessions/{session_id}/control/ws",
		"/api/v1/playback/start",
		"/api/v1/playback/transcode/{session_id}/master.m3u8",
		"/api/v1/playback/transcode/{session_id}/segment/{name}",
		"/api/v1/playback/{session_id}",
		"/api/v1/playback/{session_id}/progress",
		"/api/v1/playback/{session_id}/replan",
		"/api/v1/profile/sections/",
		"/api/v1/profile/sections/flags",
		"/api/v1/profile/sections/reset",
		"/api/v1/profile/sections/settings",
		"/api/v1/profiles/{id}",
		"/api/v1/profiles/{id}/avatar",
		"/api/v1/profiles/{id}/verify-pin",
		"/api/v1/progress/",
		"/api/v1/ratings/",
		"/api/v1/ratings/{item_id}",
		"/api/v1/ready",
		"/api/v1/recommendations/because-watched/{item_id}",
		"/api/v1/recommendations/discover",
		"/api/v1/recommendations/for-you/main",
		"/api/v1/recommendations/for-you/rows",
		"/api/v1/recommendations/popular",
		"/api/v1/recommendations/recently-added",
		"/api/v1/recommendations/section/{kind}",
		"/api/v1/recommendations/section/{kind}/{key}",
		"/api/v1/recommendations/similar-users",
		"/api/v1/recommendations/similar/{item_id}",
		"/api/v1/recommendations/taste-profile",
		"/api/v1/recommendations/taste-seed",
		"/api/v1/recommendations/taste-seed/items",
		"/api/v1/recommendations/watch-tonight",
		"/api/v1/recommendations/watch-tonight/cards",
		"/api/v1/sections/recipes",
		"/api/v1/sections/recipes/{type}/candidates",
		"/api/v1/settings/capability",
		"/api/v1/settings/contract",
		"/api/v1/settings/contract/capabilities",
		"/api/v1/settings/device/subtitle_appearance",
		"/api/v1/settings/device/{key}",
		"/api/v1/settings/effective",
		"/api/v1/settings/manifest",
		"/api/v1/settings/overlay-config",
		"/api/v1/settings/subtitle_appearance/effective",
		"/api/v1/settings/values",
		"/api/v1/settings/values/effective",
		"/api/v1/settings/values/nav.shortcuts/item",
		"/api/v1/settings/values/{key}",
		"/api/v1/stream/{session_id}",
		"/api/v1/stream/{session_id}/subtitles/{track}",
		"/api/v1/stream/{session_id}/subtitles/{track}/fonts",
		"/api/v1/subtitle-prefs/{series_id}",
		"/api/v1/subtitles/providers/status",
		"/api/v1/sync/progress",
		"/api/v1/user/libraries",
		"/api/v1/watch/{id}",
		"/api/v1/watched/{id}",
		"/api/v1/watchlist/",
		"/api/v1/watchlist/{item_id}",
		"/api/v1/works/{work_id}",
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
	for _, pattern := range []string{
		"/api/v1/auth/me",
		"/api/v1/auth/sessions",
		"/api/v1/auth/sessions/{id}",
		"/api/v1/auth/device/approve",
		"/api/v1/auth/device/deny",
		"/api/v1/auth/impersonation/end",
		"/api/v1/auth/plugin-launch",
		"/api/v1/api-keys/",
		"/api/v1/profiles/",
		"/api/v1/profiles/household/sessions",
		"/api/v1/settings/",
		"/api/v1/settings/{key}",
		"/api/v1/compat/connect-info",
		"/api/v1/diagnostics/status",
		"/api/v1/history-imports/sources",
		"/api/v1/plex-sync/connections",
		"/api/v1/webhook-sync/connections",
		"/api/v1/invitations/{token}/",
		"/api/v1/onboarding/state",
		"/api/v1/requests/",
		"/api/v1/admin/users",
		"/api/v1/livetv/channels",
	} {
		if directProfileRouteAllowed(pattern) {
			t.Errorf("%s is inside the direct-profile surface and should not be", pattern)
		}
	}
}

// Every allowlist entry has to correspond to a route that exists, or the list
// is describing a router that no longer does.
func TestDirectProfileAllowlistHasNoStaleEntries(t *testing.T) {
	routes := newRouteInventoryRouter(t)
	registered := map[string]bool{}
	for _, pattern := range walkRoutePatterns(t, routes) {
		registered[pattern] = true
	}
	for pattern := range directProfileAllowedRoutes {
		if !registered[pattern] {
			t.Errorf("allowlist names %s, which the router does not register", pattern)
		}
	}
}
