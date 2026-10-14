// Package previewcontext carries Bloem's preview presentation boundary without
// coupling catalog and artwork services to HTTP authentication.
package previewcontext

import "context"

type key struct{}

func With(ctx context.Context) context.Context { return context.WithValue(ctx, key{}, true) }
func Active(ctx context.Context) bool          { active, _ := ctx.Value(key{}).(bool); return active }
