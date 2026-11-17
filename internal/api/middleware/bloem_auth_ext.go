package middleware

// Bloem-owned. The gates RequireAuth applies to an authenticated request
// beyond Silo's: preview principals and direct-profile sessions.

import (
	"context"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// bloemAuthMiddlewareExt holds AuthMiddleware's Bloem state.
type bloemAuthMiddlewareExt struct {
	// directProfileRoutes decides whether a direct-profile session may reach
	// the route a request matched. Nil fails closed: every direct-profile
	// request is refused until a guard is installed. A fixture that wants an
	// unrestricted middleware installs an explicit allow-all guard.
	directProfileRoutes DirectProfileRouteGuard
	// Preview principals (bloem_preview.go). Nil routes fail closed.
	previewPrincipals PreviewPrincipals
	previewRoutes     PreviewRouteGuard
	previewGuests     auth.PreviewGuestSettingsSource
	previewGuestLimit *PreviewGuestLimiter
}

// DirectProfileRouteGuard reports whether a direct-profile session may use the
// route a request matches.
type DirectProfileRouteGuard func(r *http.Request) bool

// SetDirectProfileRouteGuard installs the direct-profile route boundary.
//
// It hangs off authentication rather than off individual routes because the
// boundary is default-deny: a direct-profile session authenticates one profile
// and may use only the routes that serve one profile. Enumerating the routes
// it may *not* use was tried and does not converge — the API has hundreds of
// routes, every new one is account-scoped until someone says otherwise, and
// three review rounds each found more escapes. Putting the decision where
// claims are resolved means a route is unreachable until it is named.
func (am *AuthMiddleware) SetDirectProfileRouteGuard(guard DirectProfileRouteGuard) {
	am.directProfileRoutes = guard
}

// bloemServeAuthenticated applies Bloem's gates after RequireAuth has
// resolved claims. A preview principal reaches only preview routes, and its
// responses outside /api/v2 are filtered; a direct-profile session reaches
// only its allowlisted routes, as the device it was bound to. It returns the
// request context to serve with, and handled=true when it has already
// answered the request (a refusal or a filtered preview response).
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
	if claims.AuthMethod == auth.AuthMethodDirectProfile {
		// Fail closed: with no guard installed, a direct-profile session
		// reaches nothing. The boundary is default-deny, and "the caller
		// forgot to install the allowlist" must not mean "every endpoint
		// is open" — a fixture that genuinely wants an unrestricted
		// middleware installs an allow-all guard on purpose.
		if am.directProfileRoutes == nil || !am.directProfileRoutes(r) {
			writeForbidden(w, "Direct profile sessions cannot use this endpoint")
			return ctx, true
		}
		// The session was bound to one device at login, and device
		// identity feeds downloads, device settings, and policy input.
		// Handlers read the X-Silo-Device-Id header, so the header is made
		// canonical here: a conflicting value is refused rather than
		// letting the session act as a device it never authenticated, and
		// an absent one is filled in from the binding so every consumer
		// sees the same identity. NormalizeClientHeaders has already
		// folded the X-Bloem-Device-Id spelling onto this name upstream,
		// so a device declared under either spelling meets this guard;
		// keep that middleware ahead of RequireAuth.
		if declared := strings.TrimSpace(r.Header.Get("X-Silo-Device-Id")); declared != "" && declared != claims.DeviceID {
			writeForbidden(w, "Device does not match this session's binding")
			return ctx, true
		}
		r.Header.Set("X-Silo-Device-Id", claims.DeviceID)
	}
	// Native structured responses use their actual Huma writer, after the
	// gates; other preview responses are filtered.
	if IsPreviewPrincipal(ctx) && !strings.HasPrefix(r.URL.Path, "/api/v2/") {
		servePreviewResponse(next, w, r.WithContext(ctx))
		return ctx, true
	}
	return ctx, false
}
