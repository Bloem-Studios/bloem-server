package middleware

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/go-chi/chi/v5"
)

// A direct-profile session authenticates one profile with its own credential,
// so the profile is a fact of the token rather than something the client picks
// per request. Legacy account sessions keep declaring an active profile with
// the X-Profile-Id header, because every profile on an account shares that one
// login; the helpers here keep those two models apart in one place instead of
// leaving each route to remember the difference.

// IsDirectProfileSession reports whether the request authenticated through
// direct profile login.
func IsDirectProfileSession(r *http.Request) bool {
	claims := GetClaims(r.Context())
	return claims != nil && claims.AuthMethod == auth.AuthMethodDirectProfile
}

// ActiveProfileID returns the profile a request acts as: the token-bound
// profile for a direct-profile session, otherwise the profile resolved earlier
// in the chain, otherwise the raw X-Profile-Id header. Callers that gate
// household management on the active profile must use this rather than reading
// the header, so a direct-profile session cannot assert a sibling.
func ActiveProfileID(r *http.Request) string {
	if claims := GetClaims(r.Context()); claims != nil && claims.AuthMethod == auth.AuthMethodDirectProfile {
		return claims.ProfileID
	}
	if id := GetProfileID(r.Context()); id != "" {
		return id
	}
	return r.Header.Get("X-Profile-Id")
}

// bindDirectProfile resolves the profile a request may act as, given the
// profile it declared. For a direct-profile session the token wins and a
// declared sibling is refused; for every other session the declared value is
// returned untouched. ok is false when the response has already been written.
func bindDirectProfile(w http.ResponseWriter, r *http.Request, declared string) (string, bool) {
	claims := GetClaims(r.Context())
	if claims == nil || claims.AuthMethod != auth.AuthMethodDirectProfile {
		return declared, true
	}
	if claims.ProfileID == "" {
		writeForbidden(w, "Direct profile session is missing its profile binding")
		return "", false
	}
	if declared != "" && declared != claims.ProfileID {
		writeForbidden(w, "Direct profile sessions cannot select another profile")
		return "", false
	}
	return claims.ProfileID, true
}

// RejectDirectProfileSession blocks account- and household-scoped surfaces for
// direct-profile sessions. Those sessions are bound to a single profile and
// have no account credential behind them, so listing siblings, managing the
// household, or administering the account's sessions is not theirs to do.
func RejectDirectProfileSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsDirectProfileSession(r) {
			writeForbidden(w, "Direct profile sessions cannot access account or household surfaces")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireOwnDirectProfile guards a per-profile route: a direct-profile session
// may only address the profile its token is bound to. Legacy account sessions
// pass through and keep their existing household permission checks.
func RequireOwnDirectProfile(param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsDirectProfileSession(r) {
				claims := GetClaims(r.Context())
				if claims.ProfileID == "" || chi.URLParam(r, param) != claims.ProfileID {
					writeForbidden(w, "Direct profile sessions can only address their own profile")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
