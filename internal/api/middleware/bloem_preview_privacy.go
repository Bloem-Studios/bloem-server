package middleware

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/clientip"
)

type previewPrivacyKey struct{}
type previewPrivacy struct{ clientIP string }

// WithPreviewPrivacy retains the client address only for transient admission
// limits, while removing it from the context read by request observers.
func WithPreviewPrivacy(ctx context.Context) context.Context {
	if IsPreviewRequest(ctx) {
		return ctx
	}
	ctx = context.WithValue(ctx, previewPrivacyKey{}, previewPrivacy{clientIP: clientip.FromContext(ctx)})
	return clientip.SetPeerContext(clientip.SetContext(ctx, ""), "")
}

// IsPreviewRequest includes the anonymous launch and public refresh paths.
func IsPreviewRequest(ctx context.Context) bool {
	_, ok := ctx.Value(previewPrivacyKey{}).(previewPrivacy)
	return ok
}

// PreviewClientIP is used only to derive anonymous rate-limit buckets.
func PreviewClientIP(ctx context.Context) string {
	if p, ok := ctx.Value(previewPrivacyKey{}).(previewPrivacy); ok {
		return p.clientIP
	}
	return clientip.FromContext(ctx)
}
