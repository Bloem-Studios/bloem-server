package middleware

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
)

// RequireLiveTVAccess checks the current resolved account/profile permission,
// independently of movie, series and book library membership.
func RequireLiveTVAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope, ok := access.GetScope(r.Context())
		if !ok || !scope.LiveTVAllowed {
			writePermissionError(w, http.StatusForbidden, "live_tv_forbidden", "Live TV access has not been granted")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireLiveTVStreamAccess is only for delivery routes. A signed stream token
// is already bound to one session; it cannot authorize browse or new tunes.
func RequireLiveTVStreamAccess(next http.Handler) http.Handler {
	checked := RequireLiveTVAccess(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsStreamTokenAuthorized(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		checked.ServeHTTP(w, r)
	})
}
