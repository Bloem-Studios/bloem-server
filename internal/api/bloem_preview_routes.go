package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
)

const previewCapabilitiesPath = "/api/bloem/v1/capabilities"

// previewAllowedRoutes is everything a preview principal may call on v1 and
// the native surface, by exact method and route pattern. Everything else is
// refused, including every write, every byte route and /livetv/**. v2
// operations are judged by apiv2's preview gate instead.
var previewAllowedRoutes = map[string][]string{
	"/api/bloem/v1/preview/events":                         {http.MethodPost},
	previewCapabilitiesPath:                                {http.MethodGet},
	"/api/bloem/v1/ambience":                               {http.MethodGet},
	"/api/bloem/v1/persons/{person_id}":                    {http.MethodGet},
	"/api/bloem/v1/catalog/items/{content_id}/collections": {http.MethodGet},
	"/api/bloem/v1/watch/home":                             {http.MethodGet},
	"/api/bloem/v1/watch/items/{content_id}":               {http.MethodGet},
	"/api/bloem/v1/watch/search":                           {http.MethodGet},
}

func previewRouteAllowed(method, pattern string) bool {
	for _, allowed := range previewAllowedRoutes[pattern] {
		if allowed == method {
			return true
		}
	}
	return false
}

// newPreviewRouteGuard resolves a request against the root router, as the
// direct-profile guard does. Only the v2 delegation pattern defers: a route
// chi serves itself under /api/v2 is judged here like any other.
func newPreviewRouteGuard(match func(*chi.Context, string, string) bool) apimw.PreviewRouteGuard {
	return func(r *http.Request) bool {
		if match == nil {
			return false
		}
		rctx := chi.NewRouteContext()
		if !match(rctx, r.Method, r.URL.Path) {
			return true // an unmatched request is a 404 either way
		}
		pattern := rctx.RoutePattern()
		if pattern == apiv2.DelegationPattern {
			return true
		}
		return previewRouteAllowed(r.Method, pattern)
	}
}
