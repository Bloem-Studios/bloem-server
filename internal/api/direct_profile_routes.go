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
// Route patterns named in more than one place: the allowlist here, and the
// acceptance tests that prove a bound profile can still reach them.
const (
	settingsValuesRoute     = "/api/v1/settings/values"
	playbackCapabilityRoute = "/api/v1/playback/capability"
	playbackStartRoute      = "/api/v1/playback/start"
	userLibrariesRoute      = "/api/v1/user/libraries"
)

var directProfileAllowedRoutes = map[string][]string{
	// Ending the session, and the routes that are not authenticated anyway.
	"/api/v1/auth/logout": allMethods,

	// The session's own profile record. Per-profile routes additionally hold
	// the session to its own profile id (RequireOwnDirectProfile), so these
	// entries admit the route, not a sibling.
	// PUT only: deleting a profile is household management, and a bound
	// profile does not delete even itself.
	"/api/v1/profiles/{id}":             {http.MethodPut},
	"/api/v1/profiles/{id}/avatar":      allMethods,
	"/api/v1/profiles/{id}/verify-pin":  allMethods,
	"/api/v1/profile/sections/":         allMethods,
	"/api/v1/profile/sections/flags":    allMethods,
	"/api/v1/profile/sections/reset":    allMethods,
	"/api/v1/profile/sections/settings": allMethods,

	// Profile-scoped settings: the canonical contract API and the device and
	// effective views. The legacy account-wide "/api/v1/settings/" list and
	// "/api/v1/settings/{key}" are deliberately absent.
	"/api/v1/settings/capability":                    allMethods,
	"/api/v1/settings/contract":                      allMethods,
	"/api/v1/settings/contract/capabilities":         allMethods,
	"/api/v1/settings/manifest":                      allMethods,
	"/api/v1/settings/overlay-config":                allMethods,
	"/api/v1/settings/effective":                     allMethods,
	"/api/v1/settings/subtitle_appearance/effective": allMethods,
	"/api/v1/settings/device/{key}":                  allMethods,
	"/api/v1/settings/device/subtitle_appearance":    allMethods,
	settingsValuesRoute:                              allMethods,
	"/api/v1/settings/values/{key}":                  allMethods,
	"/api/v1/settings/values/effective":              allMethods,
	"/api/v1/settings/values/nav.shortcuts/item":     allMethods,

	// Playback. These are the routes a bound profile needs to actually watch
	// something: negotiate, start, replan, report progress, and stop. The
	// transcode and stream delivery routes authorize on the session id rather
	// than the caller, and are admitted for the same session the profile just
	// started.
	playbackCapabilityRoute:                                  allMethods,
	playbackStartRoute:                                       allMethods,
	"/api/v1/playback/route-events":                          allMethods,
	"/api/v1/playback/{session_id}":                          allMethods,
	"/api/v1/playback/{session_id}/replan":                   allMethods,
	"/api/v1/playback/{session_id}/progress":                 allMethods,
	"/api/v1/playback/sessions/{session_id}/control/ws":      allMethods,
	"/api/v1/playback/transcode/{session_id}/master.m3u8":    allMethods,
	"/api/v1/playback/transcode/{session_id}/segment/{name}": allMethods,
	"/api/v1/stream/{session_id}":                            allMethods,
	"/api/v1/stream/{session_id}/subtitles/{track}":          allMethods,
	"/api/v1/stream/{session_id}/subtitles/{track}/fonts":    allMethods,

	// The client's library bootstrap.
	userLibrariesRoute: allMethods,

	// The viewer's own device registry.
	"/api/v1/devices/":                     allMethods,
	"/api/v1/devices/{device_id}":          allMethods,
	"/api/v1/devices/{device_id}/settings": allMethods,
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
	"/api/v1/ebooks",
	// Per-series and per-library playback preferences hang off the profile.
	"/api/v1/audio-prefs",
	"/api/v1/subtitle-prefs",
	"/api/v1/library-playback-prefs",
	"/api/v1/sync",
	"/api/v1/health",
	"/api/v1/ready",
}

// allMethods marks a pattern whose every registered method is profile-scoped.
var allMethods = []string{"*"}

// directProfileRouteAllowed reports whether a resolved route is part of the
// direct-profile surface. Method and pattern are both significant: a path can
// be profile-scoped for one verb and account-scoped for another, and
// /api/v1/profiles/{id} is exactly that — a bound profile edits itself but does
// not delete itself.
func directProfileRouteAllowed(method, pattern string) bool {
	if pattern == "" {
		return false
	}
	if methods, ok := directProfileAllowedRoutes[pattern]; ok {
		for _, allowed := range methods {
			if allowed == "*" || allowed == method {
				return true
			}
		}
		return false
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
		return directProfileRouteAllowed(r.Method, rctx.RoutePattern())
	}
}
