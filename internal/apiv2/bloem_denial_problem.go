package apiv2

import "net/http"

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
