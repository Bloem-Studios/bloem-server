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
// Entries are exact chi route patterns and methods rather than path prefixes:
// a path alone cannot separate editing your own profile from deleting it.

// readOnlySubtrees admit GET and HEAD on every route beneath them. Empty
// since Silo retired /api/v1 (Silo #3200), where every subtree lived; a
// direct-profile session reads nothing on /api/v2.
var readOnlySubtrees []string

// directProfileAllowedRoutes admits exact routes: every mutation, plus the
// reads that sit outside a read-only subtree.
var directProfileAllowedRoutes = map[string][]string{
	// Bloem audiobook and ebook requests carry strict account/profile/
	// organization attribution: each is filed, read and withdrawn as the bound
	// profile, and a bound profile is a user of Bloem like any other. Approval,
	// retry and setup stay administrative and are absent.
	"/api/bloem/v1/book-requests/capability":   {http.MethodGet},
	"/api/bloem/v1/book-requests/mine":         {http.MethodGet},
	"/api/bloem/v1/book-requests":              {http.MethodPost},
	"/api/bloem/v1/book-requests/{id}":         {http.MethodGet},
	"/api/bloem/v1/book-requests/{id}/cancel":  {http.MethodPost},
	"/api/bloem/v1/ebook-requests/capability":  {http.MethodGet},
	"/api/bloem/v1/ebook-requests/mine":        {http.MethodGet},
	"/api/bloem/v1/ebook-requests":             {http.MethodPost},
	"/api/bloem/v1/ebook-requests/{id}":        {http.MethodGet},
	"/api/bloem/v1/ebook-requests/{id}/cancel": {http.MethodPost},
}

// directProfileDeniedRoutes are routes inside an admitted read subtree that
// must nevertheless be refused. Empty today: the library collection views,
// once listed here, turned out to filter on a per-profile visibility row
// (user_personal_collection_profiles) and are legitimate browse results for a
// bound profile. The mechanism stays for the next read that is not.
var directProfileDeniedRoutes = map[string]bool{}

// directProfileRouteAllowed reports whether a resolved route is part of the
// direct-profile surface. Method and pattern are both significant: a path can
// be profile-scoped for one verb and not another, and a profile record is
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
// allowlist about it. Only the read-only Match method is retained; the
// registration surface never escapes the router constructor.
func newDirectProfileRouteGuard(match func(*chi.Context, string, string) bool) apimw.DirectProfileRouteGuard {
	return func(r *http.Request) bool {
		// Always resolve against the root router rather than reading the
		// in-flight route context: authentication runs partway through
		// routing, where the pattern is still a partial one like
		// "/api/v1/*", and matching that against the allowlist would refuse
		// everything.
		if match == nil {
			return false
		}
		rctx := chi.NewRouteContext()
		if !match(rctx, r.Method, r.URL.Path) {
			// An unmatched request is a 404 either way; refusing it here would
			// turn every typo into a 403.
			return true
		}
		return directProfileRouteAllowed(r.Method, rctx.RoutePattern())
	}
}
