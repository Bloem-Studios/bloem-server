package middleware

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
)

// bloemPolicyRequest gives Silo's EffectivePolicyForUser call in the
// permission gates a context naming the request's declared profile, whose
// organization group applies, and requiring the validated request tenant
// (no fallback to the no-group policy). The gate puts the original request
// back right after the call, so nothing downstream sees this context.
func bloemPolicyRequest(r *http.Request, declaredProfileID string) *http.Request {
	return r.WithContext(access.WithPolicyProfile(access.RequireTenantPolicy(r.Context()), declaredProfileID))
}
