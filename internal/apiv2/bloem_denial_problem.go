package apiv2

import (
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

// WriteBloemDenial answers a Silo /api/v2 route from a Bloem guard that runs
// ahead of the v2 router, so Silo's gate translation (gates.go) never sees its
// refusal. The answer is Silo's problem type t; Bloem's own reason rides in
// X-Bloem-Denial, as it does for the gate's preview_only refusal, and never in
// the body. retryAfter > 0 adds Retry-After.
func WriteBloemDenial(w http.ResponseWriter, r *http.Request, t ProblemType, detail, denial string, retryAfter int) {
	p := NewProblem(t, detail)
	if denial != "" {
		p = p.WithHeader("X-Bloem-Denial", denial)
	}
	if retryAfter > 0 {
		p = p.WithRetryAfter(retryAfter)
	}
	writeProblem(w, r, p)
}

// bloemDenialProblem translates a denial reason a Bloem gate recorded inside
// the v2 gate chain (denialWriter.problem), or returns nil for Silo's own
// translation. Bloem's code rides in X-Bloem-Denial, never in the body.
func bloemDenialProblem(reason string) *Problem {
	switch reason {
	case apimw.ReasonPreviewOnly:
		return NewProblem(TypePermissionDenied, "This is not available in the preview.").
			WithHeader("X-Bloem-Denial", apimw.CodePreviewOnly)
	case apimw.ReasonTenantUnavailable:
		// The membership lookup did not answer: the client keeps its session
		// and retries, as for an unavailable credential check.
		return NewProblem(TypeDependencyUnavailable, "The account's organization could not be checked right now; retry after the Retry-After delay.").
			WithHeader("X-Bloem-Denial", apimw.CodeTenantUnavailable).
			WithRetryAfter(apimw.CredentialCheckRetryAfterSeconds)
	default:
		return nil
	}
}
