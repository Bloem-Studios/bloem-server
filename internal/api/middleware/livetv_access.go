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
//
// It still requires the Live TV permission. RequireViewerAccess resolves a fresh
// scope from the token's uid/pid on every stream-token request, so the check
// reads current policy: revoking Live TV ends delivery on the next playlist or
// segment fetch instead of letting an unexpired token keep streaming.
func RequireLiveTVStreamAccess(next http.Handler) http.Handler {
	return RequireLiveTVAccess(next)
}

// StreamTokenViewer returns the viewer a stream-token request was scoped to:
// the account from the freshly resolved scope and the profile the token names.
// ok is false for requests a stream token did not authorize, or whose scope
// was not resolved, so callers never fall back to an unowned read.
func StreamTokenViewer(r *http.Request) (userID int, profileID string, ok bool) {
	if !IsStreamTokenAuthorized(r.Context()) {
		return 0, "", false
	}
	scope, found := access.GetScope(r.Context())
	if !found || scope.UserID <= 0 {
		return 0, "", false
	}
	return scope.UserID, scope.ProfileID, true
}
