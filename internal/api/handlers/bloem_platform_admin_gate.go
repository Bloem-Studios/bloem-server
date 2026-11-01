package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// RequireBloemPlatformAdmin admits the platform administration a storefront
// performs under /api/bloem/v1/admin/platform: a Platform administrative
// context, or an administrator's unscoped API key. It runs after the
// credential itself was validated (bloemEitherPlatformCredential), and checks
// again that the account is still an enabled administrator, as the upgrade
// prompt does. Organization contexts, scoped keys and ordinary account
// sessions are refused.
func RequireBloemPlatformAdmin(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, ok := bloemPlatformAdminActor(r)
			if !ok {
				writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
				return
			}
			if pool == nil {
				writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Administrative authority could not be checked.")
				return
			}
			var enabledAdmin bool
			err := pool.QueryRow(r.Context(), `SELECT COALESCE(enabled, false) AND role = 'admin' FROM users WHERE id = $1`, actor).Scan(&enabledAdmin)
			switch {
			case errors.Is(err, pgx.ErrNoRows) || err == nil && !enabledAdmin:
				writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Platform administrator authority required.")
				return
			case err != nil:
				writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Administrative authority could not be checked.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bloemPlatformAdminActor names the account behind a platform credential: a
// Platform context's account, or the owner of an administrator's unscoped API
// key.
func bloemPlatformAdminActor(r *http.Request) (int, bool) {
	if claims, ok := middleware.GetAdminContextClaims(r.Context()); ok {
		return claims.AccountID, claims.Scope == auth.AdminScopePlatform && claims.AccountID > 0
	}
	c := middleware.GetClaims(r.Context())
	if c != nil && c.TokenType == auth.TokenTypeAPIKey && c.Role == "admin" && c.UserID > 0 && len(c.APIKeyScopes) == 0 {
		return c.UserID, true
	}
	return 0, false
}

// BloemPlatformAccountResource serves an account's profiles, devices and
// sign-in sessions at /admin/platform/accounts/{account_id}/..., through the
// same handlers as the legacy /api/v1/admin/users/{user_id}/... routes. Those
// read the account from {user_id}, so the native {account_id} is passed on
// under that name. Platform scope sees the whole account; no organization
// filter applies (an account belongs to one organization).
func BloemPlatformAccountResource(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			rctx.URLParams.Add("user_id", chi.URLParam(r, "account_id"))
		}
		next(w, r)
	}
}
