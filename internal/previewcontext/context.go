// Package previewcontext carries Bloem's preview presentation boundary without
// coupling catalog and artwork services to HTTP authentication.
package previewcontext

import "context"

type key struct{}

func With(ctx context.Context) context.Context { return context.WithValue(ctx, key{}, true) }
func Active(ctx context.Context) bool          { active, _ := ctx.Value(key{}).(bool); return active }

type freePlayKey struct{}

// WithFreePlay marks a guest request whose session holds a live free-play
// grant. Only guest admission sets it, after checking the grant.
func WithFreePlay(ctx context.Context, grantID string) context.Context {
	return context.WithValue(ctx, freePlayKey{}, grantID)
}

// FreePlayGrant names the live grant of a marked request.
func FreePlayGrant(ctx context.Context) (string, bool) {
	grant, _ := ctx.Value(freePlayKey{}).(string)
	return grant, grant != ""
}
