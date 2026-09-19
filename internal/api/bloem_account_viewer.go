package api

import (
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// Account administration remains usable without choosing a viewing profile.
// When a caller does select one, resolve its account's legacy tenant before
// invoking the shared viewer/PIN guard; that guard must not evaluate a profile
// group with missing tenant authority. Direct-profile sessions are rejected by
// the route before this adapter runs.
func bloemAccountProfileViewer(tenant *apimw.TenantMiddleware, viewer *apimw.ViewerAccessMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		validated := optionalProfileViewerAccess(viewer)(next)
		if tenant == nil || viewer == nil {
			return validated
		}
		withTenant := tenant.ResolveLegacy(validated)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSpace(r.Header.Get("X-Profile-Id")) == "" {
				next.ServeHTTP(w, r)
				return
			}
			withTenant.ServeHTTP(w, r)
		})
	}
}
