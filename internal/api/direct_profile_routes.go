package api

import (
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/go-chi/chi/v5"
)

// directProfileAllowedRoutes is the whole surface a direct-profile session may
// reach. Everything not named here is refused.
//
// A direct-profile credential authenticates one profile, not the account that
// owns it, so the session gets the profile's own view: browsing, playback, its
// own progress and library state, its own preferences, and its own profile
// record. Anything that reads or changes the account — its other profiles, its
// sessions, its keys, its integrations, its server settings — is not the
// session's to touch, and neither is anything that mints a differently scoped
// credential.
//
// Entries are exact chi route patterns rather than path prefixes: a prefix
// cannot tell "/api/v1/profiles/{id}" from "/api/v1/profiles/household/
// sessions", and getting that distinction wrong is the whole risk here.
// Patterns are matched against the route the request actually resolved to, so
// this list cannot drift from the router without the inventory test noticing.
// settingsValuesRoute is the canonical profile-scoped settings collection.
const settingsValuesRoute = "/api/v1/settings/values"

var directProfileAllowedRoutes = map[string]bool{
	// Ending the session, and the routes that are not authenticated anyway.
	"/api/v1/auth/logout": true,

	// The session's own profile record. Per-profile routes additionally hold
	// the session to its own profile id (RequireOwnDirectProfile), so these
	// entries admit the route, not a sibling.
	"/api/v1/profiles/{id}":             true,
	"/api/v1/profiles/{id}/avatar":      true,
	"/api/v1/profiles/{id}/verify-pin":  true,
	"/api/v1/profile/sections/":         true,
	"/api/v1/profile/sections/flags":    true,
	"/api/v1/profile/sections/reset":    true,
	"/api/v1/profile/sections/settings": true,

	// Profile-scoped settings: the canonical contract API and the device and
	// effective views. The legacy account-wide "/api/v1/settings/" list and
	// "/api/v1/settings/{key}" are deliberately absent.
	"/api/v1/settings/capability":                    true,
	"/api/v1/settings/contract":                      true,
	"/api/v1/settings/contract/capabilities":         true,
	"/api/v1/settings/manifest":                      true,
	"/api/v1/settings/overlay-config":                true,
	"/api/v1/settings/effective":                     true,
	"/api/v1/settings/subtitle_appearance/effective": true,
	"/api/v1/settings/device/{key}":                  true,
	"/api/v1/settings/device/subtitle_appearance":    true,
	settingsValuesRoute:                              true,
	"/api/v1/settings/values/{key}":                  true,
	"/api/v1/settings/values/effective":              true,
	"/api/v1/settings/values/nav.shortcuts/item":     true,

	// The viewer's own device registry.
	"/api/v1/devices/":                     true,
	"/api/v1/devices/{device_id}":          true,
	"/api/v1/devices/{device_id}/settings": true,
}

// directProfileAllowedPrefixes covers the browsing, playback, and profile
// library surfaces, which are large, uniformly profile-scoped, and already
// resolved through viewer access. They are listed as whole subtrees because
// admitting a subtree is the intent; the inventory test pins which subtrees.
var directProfileAllowedPrefixes = []string{
	"/api/v1/items",
	"/api/v1/library",
	"/api/v1/catalog",
	"/api/v1/collections",
	"/api/v1/works",
	"/api/v1/metadata",
	"/api/v1/home",
	"/api/v1/sections",
	"/api/v1/recommendations",
	"/api/v1/calendar",
	"/api/v1/watch",
	"/api/v1/watched",
	"/api/v1/watchlist",
	"/api/v1/favorites",
	"/api/v1/history",
	"/api/v1/progress",
	"/api/v1/ratings",
	"/api/v1/downloads",
	"/api/v1/direct-download",
	"/api/v1/subtitles",
	// Per-series and per-library playback preferences hang off the profile.
	"/api/v1/audio-prefs",
	"/api/v1/subtitle-prefs",
	"/api/v1/library-playback-prefs",
	"/api/v1/sync",
	"/api/v1/health",
	"/api/v1/ready",
}

// directProfileRouteAllowed reports whether a resolved route pattern is part of
// the direct-profile surface.
func directProfileRouteAllowed(pattern string) bool {
	if pattern == "" {
		return false
	}
	if directProfileAllowedRoutes[pattern] {
		return true
	}
	for _, prefix := range directProfileAllowedPrefixes {
		if pattern == prefix || strings.HasPrefix(pattern, prefix+"/") {
			return true
		}
	}
	return false
}

// newDirectProfileRouteGuard resolves the route a request matches and asks the
// allowlist about it. The router is taken as a function because the guard is
// installed while the router is still being built; by the time a request
// arrives it is complete.
func newDirectProfileRouteGuard(routes func() chi.Routes) apimw.DirectProfileRouteGuard {
	return func(r *http.Request) bool {
		// Always resolve against the root router rather than reading the
		// in-flight route context: authentication runs partway through
		// routing, where the pattern is still a partial one like
		// "/api/v1/*", and matching that against the allowlist would refuse
		// everything.
		mux := routes()
		if mux == nil {
			return false
		}
		rctx := chi.NewRouteContext()
		if !mux.Match(rctx, r.Method, r.URL.Path) {
			// An unmatched request is a 404 either way; refusing it here would
			// turn every typo into a 403.
			return true
		}
		return directProfileRouteAllowed(rctx.RoutePattern())
	}
}
