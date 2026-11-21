package api

import (
	"net/http"
	"strconv"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/apiv2"
)

// previewGuardRefusal is one refusal of the preview guards
// (bloem_preview_host.go, bloem_preview_login.go). They run ahead of routing,
// so on a Silo /api/v2 route the refusal is written as Silo's problem type
// with Bloem's code in X-Bloem-Denial: a Silo app reads it like any other v2
// refusal. Bloem's own routes keep the guard's JSON body.
type previewGuardRefusal struct {
	status int
	// body is the guard's JSON for every route outside /api/v2.
	body string
	// legacyRetryAfter is the Retry-After that body carries, in seconds.
	legacyRetryAfter int

	problem apiv2.ProblemType
	detail  string
	denial  string
	// retryAfter is the problem's Retry-After, in seconds.
	retryAfter int
}

var (
	previewRefusalBusy = previewGuardRefusal{
		status: http.StatusTooManyRequests, legacyRetryAfter: 1,
		body:    `{"error":"rate_limited","message":"The preview is busy; try again shortly."}`,
		problem: apiv2.TypeRateLimited, detail: "The preview is busy; try again shortly.", denial: "preview_busy", retryAfter: 1,
	}
	previewRefusalHostUnavailable = previewGuardRefusal{
		status:  http.StatusServiceUnavailable,
		body:    `{"error":"unavailable","message":"Preview host configuration is unavailable."}`,
		problem: apiv2.TypeDependencyUnavailable, detail: "Preview host configuration is unavailable; retry after the Retry-After delay.",
		retryAfter: apimw.CredentialCheckRetryAfterSeconds,
	}
	previewRefusalSessionInvalid = previewGuardRefusal{
		status:  http.StatusUnauthorized,
		body:    `{"error":"unauthorized","code":"preview_session_invalid","message":"This preview session is not valid here."}`,
		problem: apiv2.TypeInvalidToken, detail: "This preview session is not valid here.", denial: apimw.CodePreviewSessionInvalid,
	}
	previewRefusalHostPath = previewGuardRefusal{
		status:  http.StatusForbidden,
		body:    `{"error":"forbidden","code":"preview_host","message":"This address only serves the preview."}`,
		problem: apiv2.TypePermissionDenied, detail: "This address only serves the preview.", denial: "preview_host",
	}
	previewRefusalHostCredential = previewGuardRefusal{
		status:  http.StatusUnauthorized,
		body:    `{"error":"unauthorized","code":"preview_host","message":"Only preview sessions are accepted at this address."}`,
		problem: apiv2.TypeInvalidToken, detail: "Only preview sessions are accepted at this address.", denial: "preview_host",
	}
	previewRefusalAccountLogin = previewGuardRefusal{
		status:  http.StatusForbidden,
		body:    `{"error":"forbidden","code":"preview_account","message":"Use a preview launch link to browse."}`,
		problem: apiv2.TypePermissionDenied, detail: "Use a preview launch link to browse.", denial: "preview_account",
	}
	previewRefusalLoginUnavailable = previewGuardRefusal{
		status:  http.StatusServiceUnavailable,
		body:    `{"error":"unavailable","message":"Sign-in is unavailable."}`,
		problem: apiv2.TypeDependencyUnavailable, detail: "Sign-in could not be checked right now; retry after the Retry-After delay.",
		retryAfter: apimw.CredentialCheckRetryAfterSeconds,
	}
)

func (f previewGuardRefusal) write(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, apiv2.Prefix+"/") {
		apiv2.WriteBloemDenial(w, r, f.problem, f.detail, f.denial, f.retryAfter)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if f.legacyRetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(f.legacyRetryAfter))
	}
	w.WriteHeader(f.status)
	_, _ = w.Write([]byte(f.body))
}
