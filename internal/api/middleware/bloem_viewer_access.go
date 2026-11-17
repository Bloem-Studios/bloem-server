package middleware

// Bloem-owned. Viewer scope for a stream-token-authorized media request.

import (
	"encoding/json"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
)

// bloemViewerAccessExt holds ViewerAccessMiddleware's Bloem state.
type bloemViewerAccessExt struct {
	// tokenResolver resolves viewer scope for a stream-token-authorized
	// request, which carries no bearer claims and passed through no tenant
	// middleware (RequireBloem/ResolveNative both skip it, same as this
	// gate used to). It must resolve a tenant fresh from the token's own
	// uid/pid rather than depend on a tenant already being in context — see
	// policy.TenantViewerResolver. Nil in wiring that never authorizes a
	// stream-token request (e.g. no JWTSecret configured), in which case
	// requireStreamTokenViewerAccess fails closed rather than silently
	// skipping as before.
	tokenResolver ViewerResolver
}

// SetTokenResolver wires the resolver used to scope a stream-token-authorized
// request (see the tokenResolver field doc). Optional; a middleware without
// one fails closed on every stream-token request rather than resolving scope.
func (m *ViewerAccessMiddleware) SetTokenResolver(resolver ViewerResolver) {
	m.tokenResolver = resolver
}

// requireStreamTokenViewerAccess resolves scope for a stream-token-authorized
// media-delivery request from the token's own uid/pid: the request carries no
// bearer claims and passed through no tenant middleware
// (RequireBloem/ResolveNative both skip a stream-token request, same as this
// gate used to). A session or its signed restart recipe names a source; it
// does not preserve a revoked library grant, so this must resolve a live
// scope rather than pass the request through ungated, which is what this
// branch did before. uid/pid are lookup keys only — see streamtoken.Claims —
// re-resolved against the current, authoritative policy on every request.
func (m *ViewerAccessMiddleware) requireStreamTokenViewerAccess(next http.Handler, w http.ResponseWriter, r *http.Request) {
	if m.tokenResolver == nil {
		writeUnauthorized(w, "Authentication required", ReasonAuthenticationRequired)
		return
	}
	claims, ok := StreamTokenClaims(r.Context())
	if !ok || claims.UserID <= 0 {
		writeUnauthorized(w, "Authentication required", ReasonAuthenticationRequired)
		return
	}
	input := access.ResolveInput{
		UserID:    claims.UserID,
		ProfileID: claims.ProfileID,
		// The stream token is only minted after playback start already
		// verified the profile PIN; a native player replaying it on every
		// byte/segment request cannot also carry a PIN, and re-litigating
		// that decision here would break every legitimate token delivery.
		SkipPINVerification: true,
	}
	scope, err := m.tokenResolver.Resolve(r.Context(), input)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(errorResponse{
			Error:   "unauthorized",
			Message: "Failed to resolve viewer access",
		})
		return
	}
	ctx := access.SetScope(r.Context(), scope)
	if claims.ProfileID != "" {
		ctx = SetProfileID(ctx, claims.ProfileID)
	}
	next.ServeHTTP(w, r.WithContext(ctx))
}
