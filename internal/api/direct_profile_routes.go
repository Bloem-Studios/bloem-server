package api

import (
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/go-chi/chi/v5"
)

// The direct-profile surface.
//
// A direct-profile credential authenticates one profile, not the account that
// owns it, and it is typed into third-party clients — so it must not be
// spendable as the account. The session may browse, play, and keep its own
// progress, settings, devices, and profile record. Household management,
// account surfaces, and anything that mints or alters a differently scoped
// credential are refused, including when the bound profile is the household
// primary.
//
// The boundary is default-deny and comes in two halves:
//
//   - Reading is admitted by subtree. The browse and library surfaces are
//     large, uniformly profile-scoped, and already resolved through viewer
//     access, so a new read under one of them is safe by construction.
//   - Writing is admitted one route at a time. A mutation is where a bound
//     profile could reach somebody else's state, so no subtree grants one: a
//     new write is refused until it appears below, which forces someone to
//     decide whether a single profile may perform it.
//
// Entries are exact chi route patterns and methods rather than path prefixes.
// A prefix cannot separate "/api/v1/profiles/{id}" from
// "/api/v1/profiles/household/sessions", and a path alone cannot separate
// editing your own profile from deleting it.

// readOnlySubtrees admit GET and HEAD on every route beneath them.
var readOnlySubtrees = []string{
	"/api/v1/items",
	"/api/v1/library",
	"/api/v1/catalog",
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
	"/api/v1/ebooks",
	"/api/v1/audio-prefs",
	"/api/v1/subtitle-prefs",
	"/api/v1/library-playback-prefs",
	"/api/v1/user",
	"/api/v1/health",
	"/api/v1/ready",
}

// Route patterns named in more than one place: here and in the tests that
// prove a bound profile can still reach them.
const (
	settingsValuesRoute     = "/api/v1/settings/values"
	playbackCapabilityRoute = "/api/v1/playback/capability"
	playbackStartRoute      = "/api/v1/playback/start"
	userLibrariesRoute      = "/api/v1/user/libraries"
)

// directProfileAllowedRoutes admits exact routes: every mutation, plus the
// reads that sit outside a read-only subtree.
var directProfileAllowedRoutes = map[string][]string{
	// Ending the session.
	"/api/v1/auth/logout": {http.MethodPost},

	// The session's own profile record. Per-profile routes additionally hold
	// the session to its own profile id (RequireOwnDirectProfile), so these
	// admit the route, not a sibling. Deleting a profile is household
	// management and is absent; so is verify-pin, which mints a profile token
	// and would let a PIN be exercised by a credential that is not the PIN.
	"/api/v1/profiles/{id}":             {http.MethodGet, http.MethodPut},
	"/api/v1/profiles/{id}/avatar":      {http.MethodPut, http.MethodDelete},
	"/api/v1/profile/sections/":         {http.MethodGet, http.MethodPut},
	"/api/v1/profile/sections/flags":    {http.MethodGet},
	"/api/v1/profile/sections/reset":    {http.MethodDelete},
	"/api/v1/profile/sections/settings": {http.MethodGet},

	// Profile-scoped settings: the contract API and the device and effective
	// views. The account-wide "/api/v1/settings/" list and
	// "/api/v1/settings/{key}" are deliberately absent.
	"/api/v1/settings/capability":                    {http.MethodGet},
	"/api/v1/settings/contract":                      {http.MethodGet},
	"/api/v1/settings/contract/capabilities":         {http.MethodGet},
	"/api/v1/settings/manifest":                      {http.MethodGet},
	"/api/v1/settings/overlay-config":                {http.MethodGet},
	"/api/v1/settings/effective":                     {http.MethodGet},
	"/api/v1/settings/subtitle_appearance/effective": {http.MethodGet},
	"/api/v1/settings/device/{key}":                  {http.MethodGet, http.MethodPut, http.MethodDelete},
	"/api/v1/settings/device/subtitle_appearance":    {http.MethodPut, http.MethodDelete},
	settingsValuesRoute:                              {http.MethodGet},
	"/api/v1/settings/values/{key}":                  {http.MethodGet, http.MethodPut, http.MethodDelete},
	"/api/v1/settings/values/effective":              {http.MethodGet, http.MethodPost},
	"/api/v1/settings/values/nav.shortcuts/item":     {http.MethodPut},

	// The viewer's own device registry.
	"/api/v1/devices/":                     {http.MethodGet},
	"/api/v1/devices/{device_id}":          {http.MethodDelete},
	"/api/v1/devices/{device_id}/settings": {http.MethodDelete},

	// Playback: negotiate, start, replan, report progress, stop. Transcode and
	// stream delivery authorize on the session id, and the handlers hold a
	// direct-profile bearer to the session its own profile started.
	playbackCapabilityRoute:                                  {http.MethodGet},
	playbackStartRoute:                                       {http.MethodPost},
	"/api/v1/playback/route-events":                          {http.MethodPost},
	"/api/v1/playback/{session_id}":                          {http.MethodDelete},
	"/api/v1/playback/{session_id}/replan":                   {http.MethodPost},
	"/api/v1/playback/{session_id}/progress":                 {http.MethodPost},
	"/api/v1/playback/sessions/{session_id}/control/ws":      {http.MethodGet},
	"/api/v1/playback/transcode/{session_id}/master.m3u8":    {http.MethodGet},
	"/api/v1/playback/transcode/{session_id}/segment/{name}": {http.MethodGet},
	"/api/v1/stream/{session_id}":                            {http.MethodGet},
	"/api/v1/stream/{session_id}/subtitles/{track}":          {http.MethodGet},
	"/api/v1/stream/{session_id}/subtitles/{track}/fonts":    {http.MethodGet},
	"/api/v1/sync/progress":                                  {http.MethodPost},

	// The client's library bootstrap.
	userLibrariesRoute: {http.MethodGet},

	// Searching the catalog is a read that happens to be a POST.
	"/api/v1/catalog/query":          {http.MethodPost},
	"/api/v1/catalog/filters/search": {http.MethodGet, http.MethodPost},

	// The profile's own library state.
	"/api/v1/favorites/{item_id}":                             {http.MethodPut, http.MethodDelete},
	"/api/v1/watchlist/{item_id}":                             {http.MethodPut, http.MethodDelete},
	"/api/v1/watched/{id}":                                    {http.MethodPost, http.MethodDelete},
	"/api/v1/ratings/{item_id}":                               {http.MethodPut, http.MethodDelete},
	"/api/v1/history/remove":                                  {http.MethodPost},
	"/api/v1/home/dismissals/{surface}/{item_id}":             {http.MethodPut, http.MethodDelete},
	"/api/v1/recommendations/taste-seed":                      {http.MethodPost},
	"/api/v1/audio-prefs/{series_id}":                         {http.MethodPut, http.MethodDelete},
	"/api/v1/subtitle-prefs/{series_id}":                      {http.MethodPut, http.MethodDelete},
	"/api/v1/library-playback-prefs/{library_id}":             {http.MethodPut, http.MethodDelete},
	"/api/v1/ebooks/{content_id}/progress":                    {http.MethodPut},
	"/api/v1/ebooks/{content_id}/reader-config":               {http.MethodPut},
	"/api/v1/ebooks/{content_id}/annotations":                 {http.MethodPost},
	"/api/v1/ebooks/{content_id}/annotations/{annotation_id}": {http.MethodPatch, http.MethodDelete},

	// The profile's own downloads.
	"/api/v1/downloads/":                   {http.MethodPost},
	"/api/v1/downloads/{id}":               {http.MethodPatch, http.MethodDelete},
	"/api/v1/downloads/subscriptions":      {http.MethodPost},
	"/api/v1/downloads/subscriptions/{id}": {http.MethodPatch, http.MethodDelete},
	"/api/v1/downloads/subscriptions/sync": {http.MethodPost},

	// Personal collections are off this surface entirely, reads included.
	// Their handlers and store scope by account alone, so neither a read nor
	// a mutation can tell two profiles of one household apart; they return
	// when collection ownership is profile-aware. The server-wide collection
	// views carry no household state and stay.
	"/api/v1/collections/server":       {http.MethodGet},
	"/api/v1/collections/capabilities": {http.MethodGet},
}

// directProfileDeniedRoutes are routes inside an admitted read subtree that
// must nevertheless be refused. Personal collections are off this surface
// until their ownership is profile-aware, and these two library reads return
// them through a different door than /collections.
var directProfileDeniedRoutes = map[string]bool{
	"/api/v1/library/{id}/user-collections": true,
	"/api/v1/library/{id}/collections":      true,
}

// directProfileRouteAllowed reports whether a resolved route is part of the
// direct-profile surface. Method and pattern are both significant: a path can
// be profile-scoped for one verb and not another, and /api/v1/profiles/{id} is
// exactly that — a bound profile reads and edits itself, and does not delete
// itself.
func directProfileRouteAllowed(method, pattern string) bool {
	if pattern == "" || directProfileDeniedRoutes[pattern] {
		return false
	}
	if methods, ok := directProfileAllowedRoutes[pattern]; ok {
		for _, allowed := range methods {
			if allowed == method {
				return true
			}
		}
		// An exact entry names the writes a pattern permits; it must not
		// swallow the reads its subtree already grants. Returning false here
		// for GET denied a profile its own favorites, ratings, downloads, and
		// preferences purely because the same path also had a listed PUT.
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	for _, subtree := range readOnlySubtrees {
		if pattern == subtree || strings.HasPrefix(pattern, subtree+"/") {
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
