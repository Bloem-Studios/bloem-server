package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// BloemNewArrivalsOutput is the recent additions to the chosen libraries.
type BloemNewArrivalsOutput struct{ Body handlers.BloemNewArrivals }

// registerBloemNewArrivalsDocument documents recent additions a storefront
// mails as "new this week" (handlers.BloemNewArrivalsHandler).
func registerBloemNewArrivalsDocument(reg *Registry) {
	op := bloemChiDocumentOp(reg, "GET", "/admin/platform/new-arrivals", "getBloemNewArrivals", "libraries", "List recent additions to libraries.", "bloemPlatformContext", http.StatusOK)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = "Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. Query: days (1-31, default 7), limit (1-50, default 20) and any number of library IDs (default every enabled library; disabled libraries never count). Lists films first seen in a chosen library inside the window and series with episodes first seen there, newest first: titles and years only, no paths, files or accounts. Responses are not cacheable."
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "invalid_request: days, limit or a library ID is out of range, or an unknown query parameter was sent.",
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled.",
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route.",
		503: "new_arrivals_unavailable: the database is unavailable or the read timed out; tenant_unavailable or service_unavailable: the credential could not be checked.",
	})
	op.Responses["200"] = &huma.Response{
		Description: http.StatusText(http.StatusOK),
		Headers:     map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}},
	}
	registerBloemChiDocument[struct{}, BloemNewArrivalsOutput](reg, op)
}
