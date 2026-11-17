package middleware

// Bloem-owned. The gates RequireAuth applies to an authenticated request
// beyond Silo's: preview principals.

import (
	"context"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// bloemAuthMiddlewareExt holds AuthMiddleware's Bloem state.
type bloemAuthMiddlewareExt struct {
	// Preview principals (bloem_preview.go). Nil routes fail closed.
	previewPrincipals PreviewPrincipals
	previewRoutes     PreviewRouteGuard
	previewGuests     auth.PreviewGuestSettingsSource
	previewGuestLimit *PreviewGuestLimiter
}

// bloemServeAuthenticated applies Bloem's gates after RequireAuth has
// resolved claims. A preview principal reaches only preview routes, and its
// responses outside /api/v2 are filtered. It returns the request context to
// serve with, and handled=true when it has already answered the request (a
// refusal or a filtered preview response).
func (am *AuthMiddleware) bloemServeAuthenticated(ctx context.Context, next http.Handler, w http.ResponseWriter, r *http.Request, claims *auth.Claims, previewOnly bool) (context.Context, bool) {
	if previewOnly {
		ctx = withPreviewPrincipal(ctx)
		if claims.AuthMethod == auth.AuthMethodPreviewGuest {
			ctx = am.markPreviewGuest(ctx, claims)
		}

		if am.previewRoutes == nil || !am.previewRoutes(r.WithContext(ctx)) {
			WritePreviewOnly(w)
			return ctx, true
		}
	}
	// Native structured responses use their actual Huma writer, after the
	// gates; other preview responses are filtered.
	if IsPreviewPrincipal(ctx) && !strings.HasPrefix(r.URL.Path, "/api/v2/") {
		servePreviewResponse(next, w, r.WithContext(ctx))
		return ctx, true
	}
	return ctx, false
}
