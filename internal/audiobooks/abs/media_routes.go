package abs

import (
	"net/http"
	"time"

	"github.com/Silo-Server/silo-server/internal/httpstream"
	"github.com/Silo-Server/silo-server/internal/streamtelemetry"
)

var absMediaRoutes = func() []streamtelemetry.MediaRoute {
	routes := []streamtelemetry.MediaRoute{
		absRoute(http.MethodGet, "/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodHead, "/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodGet, "/abs/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodHead, "/abs/public/session/{sid}/track/{idx}", streamtelemetry.ClassPlayback, true, "session_id"),
		absRoute(http.MethodGet, "/feed/{slug}/file/{ino}", streamtelemetry.ClassTransfer, false, "feed_owner"),
	}
	for _, prefix := range []string{canonicalAPIPrefix, legacyAPIPrefix} {
		routes = append(routes,
			absRoute(http.MethodGet, prefix+"/items/{libraryItemId}/file/{ino}", streamtelemetry.ClassTransfer, false, "abs_user"),
			absRoute(http.MethodGet, prefix+"/items/{libraryItemId}/file/{ino}/download", streamtelemetry.ClassTransfer, false, "abs_user"),
			absRoute(http.MethodGet, prefix+"/items/{id}/ebook/{fileid}", streamtelemetry.ClassTransfer, false, "abs_user"),
		)
	}
	return routes
}()

func absRoute(method, pattern string, class streamtelemetry.Class, capRelevant bool, key string) streamtelemetry.MediaRoute {
	return streamtelemetry.MediaRoute{Family: streamtelemetry.FamilyABS, Method: method, Pattern: pattern,
		Class: class, Role: streamtelemetry.RoleViewerEgress, CanonicalSessionKey: key,
		CapRelevant: capRelevant, Enrolled: true, Capture: absCapture(pattern)}
}

// absCapture records the §2.2 request-time set for an ABS client.
//
// Client identity reuses absPlaybackClientInfoFromRequest (native_sessions.go),
// which the package already trusts for native-session mirroring — telemetry must
// not establish a second, poorer client-identity policy for the same headers.
// DeviceID stays empty: the bearer context (ctxAuth) does not carry the JWT's
// optional device id, and there is no other honest source.
func absCapture(pattern string) func(*http.Request) streamtelemetry.CaptureSet {
	return func(r *http.Request) streamtelemetry.CaptureSet {
		client := absPlaybackClientInfoFromRequest(r)
		return streamtelemetry.CaptureSet{
			Method: r.Method, Pattern: pattern, ViewerIP: requestClientIP(r),
			Client: streamtelemetry.ClientVariant{
				Name: client.Name, Version: client.Version, Build: client.Build, Channel: client.Channel,
			},
			UserAgent: client.UserAgent, ReceivedAt: time.Now(),
		}
	}
}

func declareABSMediaRoutes() { streamtelemetry.DeclareRoutes(absMediaRoutes...) }

func absMediaRoute(method, pattern string) streamtelemetry.MediaRoute {
	for _, route := range absMediaRoutes {
		if route.Method == method && route.Pattern == pattern {
			return route
		}
	}
	panic("undeclared abs media route: " + method + " " + pattern)
}

// observeABS wraps one ABS media handler.
//
// Wrapping PER ROUTE is deliberate and load-bearing. Mount puts h.accessLog on a
// group covering every route — media and socket.io alike (handler.go:362) — so
// adding telemetry as another r.Use there would put an extra ResponseWriter
// between engine.io and the raw connection. §4.4: "ABS mounts one access-log
// wrapper across both media and socket.io, so middleware placement decides
// whether websockets survive."
func observeABS(registry *streamtelemetry.Registry, method, pattern string, handler http.HandlerFunc) http.HandlerFunc {
	var observed http.Handler = handler
	if registry != nil {
		observed = registry.Observe(absMediaRoute(method, pattern))(handler)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// Only the compatibility gateway supplies this trusted mount context.
		// Dedicated ABS listeners keep their existing zero-WriteTimeout path,
		// while mounted media replaces the public server's absolute deadline
		// before the handler can commit response headers.
		if mount, _ := r.Context().Value(absPublicMountContextKey{}).(string); mount != "" {
			w = httpstream.NewRollingDeadlineWriter(w)
		}
		observed.ServeHTTP(w, r)
	}
}
