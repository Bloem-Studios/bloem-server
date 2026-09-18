package handlers

import (
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// RequireBloemPlatformContext follows AdminContextMiddleware.Require. It never
// exchanges an account or organization credential for platform authority.
func RequireBloemPlatformContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := apimw.GetAdminContextClaims(r.Context())
		if !ok || claims.Scope != auth.AdminScopePlatform || claims.AccountID <= 0 {
			writeError(w, http.StatusForbidden, "insufficient_platform_authority", "Select a platform administrative context")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func engagementActorID(r *http.Request) int {
	if claims, ok := apimw.GetAdminContextClaims(r.Context()); ok && claims.Scope == auth.AdminScopePlatform {
		return claims.AccountID
	}
	return apimw.GetUserID(r.Context())
}
