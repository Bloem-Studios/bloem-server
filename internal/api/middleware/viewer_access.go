package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// ViewerResolver resolves the effective viewer access scope for a request.
type ViewerResolver interface {
	Resolve(ctx context.Context, input access.ResolveInput) (access.Scope, error)
}

// ViewerAccessMiddleware resolves and stores viewer access scope in context.
type ViewerAccessMiddleware struct {
	resolver ViewerResolver
	// tokenResolver resolves viewer scope for a stream-token-authorized
	// request, which carries no bearer claims and passed through no tenant
	// middleware (RequireBloem/ResolveNative both skip it, same as this
	// gate used to). It must resolve a tenant fresh from the token's own
	// uid/pid rather than depend on a tenant already being in context — see
	// policy.TenantViewerResolver. Nil in wiring that never authorizes a
	// stream-token request (e.g. no JWTSecret configured), in which case the
	// branch below fails closed rather than silently skipping as before.
	tokenResolver ViewerResolver
}

// NewViewerAccessMiddleware creates a middleware from a scope resolver.
func NewViewerAccessMiddleware(resolver ViewerResolver) *ViewerAccessMiddleware {
	return &ViewerAccessMiddleware{resolver: resolver}
}

// SetTokenResolver wires the resolver used to scope a stream-token-authorized
// request (see the tokenResolver field doc). Optional; a middleware without
// one fails closed on every stream-token request rather than resolving scope.
func (m *ViewerAccessMiddleware) SetTokenResolver(resolver ViewerResolver) {
	m.tokenResolver = resolver
}

// RequireViewerAccess resolves viewer scope from auth + profile headers.
func (m *ViewerAccessMiddleware) RequireViewerAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsStreamTokenAuthorized(r.Context()) {
			m.requireStreamTokenViewerAccess(next, w, r)
			return
		}
		claims := GetClaims(r.Context())
		if claims == nil {
			writeUnauthorized(w, "Authentication required", ReasonAuthenticationRequired)
			return
		}

		profileID, ok := bindDirectProfile(w, r, r.Header.Get("X-Profile-Id"))
		if !ok {
			return
		}
		input := access.ResolveInput{
			UserID:              claims.UserID,
			SessionID:           claims.SessionID,
			ProfileID:           profileID,
			ProfileToken:        r.Header.Get("X-Profile-Token"),
			SkipPINVerification: claims.TokenType == auth.TokenTypeAPIKey || claims.TokenType == auth.TokenTypeApplePushDisplay || claims.AuthMethod == auth.AuthMethodDirectProfile || claims.AuthMethod == auth.AuthMethodAudienceTicket,
		}

		scope, err := m.resolver.Resolve(r.Context(), input)
		if err != nil {
			switch {
			case errors.Is(err, access.ErrProfileUnverified):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(errorResponse{
					Error:   "profile_unverified",
					Message: "Profile verification required",
				})
				return
			case errors.Is(err, access.ErrProfileNotFound):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(errorResponse{
					Error:   "not_found",
					Message: "Profile not found",
				})
				return
			default:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(errorResponse{
					Error:   "internal_error",
					Message: "Failed to resolve viewer access",
				})
				return
			}
		}

		ctx := access.SetScope(r.Context(), scope)
		if profileID != "" {
			ctx = SetProfileID(ctx, profileID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
