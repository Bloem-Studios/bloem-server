package middleware

import (
	"context"
	"net/http"
)

// CodePreviewOnly is the refusal code a preview principal receives, in the
// v1 body and the X-Bloem-Denial header (v2 maps it to permission_denied).
const (
	CodePreviewOnly   = "preview_only"
	ReasonPreviewOnly = "preview_only"
)

// PreviewPrincipals resolves whether a caller is a preview principal.
// *auth.SessionRepository implements it.
type PreviewPrincipals interface {
	ActiveSessionAuthority(ctx context.Context, sessionID string) (role string, previewOnly, active bool, err error)
	AccountPreviewOnly(ctx context.Context, accountID int) (bool, error)
}

// PreviewRouteGuard reports whether a preview principal may use the route a
// request resolves to. Nil denies everything.
type PreviewRouteGuard func(r *http.Request) bool

// SetPreviewPrincipals installs the preview lookup. Without it no caller is
// treated as a preview principal, so production wiring must install it.
func (am *AuthMiddleware) SetPreviewPrincipals(p PreviewPrincipals) { am.previewPrincipals = p }

// SetPreviewRouteGuard installs the v1 and native preview route boundary.
func (am *AuthMiddleware) SetPreviewRouteGuard(guard PreviewRouteGuard) { am.previewRoutes = guard }

type previewPrincipalKey struct{}

// IsPreviewPrincipal reports whether RequireAuth authenticated the request as
// a preview principal.
func IsPreviewPrincipal(ctx context.Context) bool {
	marked, _ := ctx.Value(previewPrincipalKey{}).(bool)
	return marked
}

func withPreviewPrincipal(ctx context.Context) context.Context {
	return context.WithValue(ctx, previewPrincipalKey{}, true)
}

// WritePreviewOnly refuses a request because the caller is a preview principal.
func WritePreviewOnly(w http.ResponseWriter) {
	if recorder, ok := w.(DenialReasonRecorder); ok {
		recorder.RecordDenialReason(ReasonPreviewOnly)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Bloem-Denial", CodePreviewOnly)
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":"forbidden","code":"preview_only","message":"This is not available in the preview."}`))
}

// sessionAuthority is the per-request session check, with the preview flag
// when a preview lookup is installed.
func (am *AuthMiddleware) sessionAuthority(ctx context.Context, sessionID string) (role string, previewOnly, active bool, err error) {
	if am.previewPrincipals != nil {
		return am.previewPrincipals.ActiveSessionAuthority(ctx, sessionID)
	}
	role, active, err = am.sessionValidator.ActiveSessionRole(ctx, sessionID)
	return role, false, active, err
}

// apiKeyOwnerPreview reports whether an API key's owner is a preview principal.
func (am *AuthMiddleware) apiKeyOwnerPreview(ctx context.Context, accountID int) (bool, error) {
	if am.previewPrincipals == nil {
		return false, nil
	}
	return am.previewPrincipals.AccountPreviewOnly(ctx, accountID)
}
