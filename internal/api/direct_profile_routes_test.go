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
		"DELETE /api/v1/audio-prefs/{series_id}",
		"DELETE /api/v1/devices/{device_id}",
		"DELETE /api/v1/devices/{device_id}/settings",
		"DELETE /api/v1/downloads/subscriptions/{id}",
		"DELETE /api/v1/downloads/{id}",
		"DELETE /api/v1/ebooks/{content_id}/annotations/{annotation_id}",
		"DELETE /api/v1/favorites/{item_id}",
		"DELETE /api/v1/home/dismissals/{surface}/{item_id}",
		"DELETE /api/v1/library-playback-prefs/{library_id}",
		"DELETE /api/v1/playback/{session_id}",
		"DELETE /api/v1/profile/sections/reset",
		"DELETE /api/v1/profiles/{id}/avatar",
		"DELETE /api/v1/ratings/{item_id}",
		"DELETE /api/v1/settings/device/subtitle_appearance",
		"DELETE /api/v1/settings/device/{key}",
		"DELETE /api/v1/settings/values/{key}",
		"DELETE /api/v1/subtitle-prefs/{series_id}",
		"DELETE /api/v1/watched/{id}",
		"DELETE /api/v1/watchlist/{item_id}",
		"GET /api/v1/audio-prefs/{series_id}",
		"GET /api/v1/calendar",
		"GET /api/v1/catalog",
		"GET /api/v1/catalog/audiobook-groups",
		"GET /api/v1/catalog/filters",
		"GET /api/v1/catalog/filters/search",
		"GET /api/v1/catalog/items/{id}",
		"GET /api/v1/catalog/items/{id}/episodes",
		"GET /api/v1/catalog/items/{id}/manga-files",
		"GET /api/v1/catalog/items/{id}/versions",
		"GET /api/v1/catalog/series/{id}/seasons",
		"GET /api/v1/catalog/series/{id}/seasons/{num}",
		"GET /api/v1/catalog/series/{id}/seasons/{num}/episodes",
		"GET /api/v1/collections/capabilities",
		"GET /api/v1/collections/server",
		"GET /api/v1/devices/",
		"GET /api/v1/direct-download",
		"GET /api/v1/downloads/",
		"GET /api/v1/downloads/batches/{batch_id}/manifests",
		"GET /api/v1/downloads/capability",
		"GET /api/v1/downloads/subscriptions",
		"GET /api/v1/downloads/subscriptions/{id}",
		"GET /api/v1/downloads/{id}/artwork/{kind}",
		"GET /api/v1/downloads/{id}/file",
		"GET /api/v1/downloads/{id}/file-proxy",
		"GET /api/v1/downloads/{id}/manifest",
		"GET /api/v1/downloads/{id}/subtitles/{ref}",
		"GET /api/v1/ebooks/capability",
		"GET /api/v1/ebooks/{content_id}/annotations",
		"GET /api/v1/ebooks/{content_id}/files/{file_id}/read",
		"GET /api/v1/ebooks/{content_id}/progress",
		"GET /api/v1/ebooks/{content_id}/reader-config",
		"GET /api/v1/favorites/",
		"GET /api/v1/favorites/{item_id}",
		"GET /api/v1/health",
		"GET /api/v1/history/",
		"GET /api/v1/home/layout",
		"GET /api/v1/home/sections",
		"GET /api/v1/home/sections/{id}/items",
		"GET /api/v1/items/trailers/capability",
		"GET /api/v1/library-playback-prefs/",
		"GET /api/v1/library/{id}/collections/{collection_id}/items",
		"GET /api/v1/library/{id}/layout",
		"GET /api/v1/library/{id}/sections",
		"GET /api/v1/library/{id}/sections/{sectionId}/items",
		"GET /api/v1/metadata/ai/status",
		"GET /api/v1/playback/capability",
		"GET /api/v1/playback/sessions/{session_id}/control/ws",
		"GET /api/v1/playback/transcode/{session_id}/master.m3u8",
		"GET /api/v1/playback/transcode/{session_id}/segment/{name}",
		"GET /api/v1/profile/sections/",
		"GET /api/v1/profile/sections/flags",
		"GET /api/v1/profile/sections/settings",
		"GET /api/v1/profiles/{id}",
		"GET /api/v1/progress/",
		"GET /api/v1/ratings/",
		"GET /api/v1/ratings/{item_id}",
		"GET /api/v1/ready",
		"GET /api/v1/recommendations/because-watched/{item_id}",
		"GET /api/v1/recommendations/discover",
		"GET /api/v1/recommendations/for-you/main",
		"GET /api/v1/recommendations/for-you/rows",
		"GET /api/v1/recommendations/popular",
		"GET /api/v1/recommendations/recently-added",
		"GET /api/v1/recommendations/section/{kind}",
		"GET /api/v1/recommendations/section/{kind}/{key}",
		"GET /api/v1/recommendations/similar-users",
		"GET /api/v1/recommendations/similar/{item_id}",
		"GET /api/v1/recommendations/taste-profile",
		"GET /api/v1/recommendations/taste-seed/items",
		"GET /api/v1/recommendations/watch-tonight",
		"GET /api/v1/recommendations/watch-tonight/cards",
		"GET /api/v1/sections/recipes",
		"GET /api/v1/sections/recipes/{type}/candidates",
		"GET /api/v1/settings/capability",
		"GET /api/v1/settings/contract",
		"GET /api/v1/settings/contract/capabilities",
		"GET /api/v1/settings/device/{key}",
		"GET /api/v1/settings/effective",
		"GET /api/v1/settings/manifest",
		"GET /api/v1/settings/overlay-config",
		"GET /api/v1/settings/subtitle_appearance/effective",
		"GET /api/v1/settings/values",
		"GET /api/v1/settings/values/effective",
		"GET /api/v1/settings/values/{key}",
		"GET /api/v1/stream/{session_id}",
		"GET /api/v1/stream/{session_id}/subtitles/{track}",
		"GET /api/v1/stream/{session_id}/subtitles/{track}/fonts",
		"GET /api/v1/subtitle-prefs/{series_id}",
		"GET /api/v1/user/libraries",
		"GET /api/v1/watch/{id}",
		"GET /api/v1/watchlist/",
		"GET /api/v1/watchlist/{item_id}",
		"GET /api/v1/works/{work_id}",
		"HEAD /api/v1/direct-download",
		"HEAD /api/v1/downloads/{id}/file",
		"HEAD /api/v1/downloads/{id}/file-proxy",
		"HEAD /api/v1/ebooks/{content_id}/files/{file_id}/read",
		"PATCH /api/v1/downloads/subscriptions/{id}",
		"PATCH /api/v1/downloads/{id}",
		"PATCH /api/v1/ebooks/{content_id}/annotations/{annotation_id}",
		"POST /api/v1/auth/logout",
		"POST /api/v1/catalog/query",
		"POST /api/v1/downloads/",
		"POST /api/v1/downloads/subscriptions",
		"POST /api/v1/downloads/subscriptions/sync",
		"POST /api/v1/ebooks/{content_id}/annotations",
		"POST /api/v1/history/remove",
		"POST /api/v1/playback/route-events",
		"POST /api/v1/playback/start",
		"POST /api/v1/playback/{session_id}/progress",
		"POST /api/v1/playback/{session_id}/replan",
		"POST /api/v1/recommendations/taste-seed",
		"POST /api/v1/settings/values/effective",
		"POST /api/v1/sync/progress",
		"POST /api/v1/watched/{id}",
		"PUT /api/v1/audio-prefs/{series_id}",
		"PUT /api/v1/ebooks/{content_id}/progress",
		"PUT /api/v1/ebooks/{content_id}/reader-config",
		"PUT /api/v1/favorites/{item_id}",
		"PUT /api/v1/home/dismissals/{surface}/{item_id}",
		"PUT /api/v1/library-playback-prefs/{library_id}",
		"PUT /api/v1/profile/sections/",
		"PUT /api/v1/profiles/{id}",
		"PUT /api/v1/profiles/{id}/avatar",
		"PUT /api/v1/ratings/{item_id}",
		"PUT /api/v1/settings/device/subtitle_appearance",
		"PUT /api/v1/settings/device/{key}",
		"PUT /api/v1/settings/values/nav.shortcuts/item",
		"PUT /api/v1/settings/values/{key}",
		"PUT /api/v1/subtitle-prefs/{series_id}",
		"PUT /api/v1/watchlist/{item_id}",
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
