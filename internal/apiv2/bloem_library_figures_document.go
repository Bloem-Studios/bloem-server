package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// BloemLibraryFiguresOutput is every library's item counts.
type BloemLibraryFiguresOutput struct{ Body handlers.BloemLibraryFigures }

// registerBloemLibraryFiguresDocument documents the per-library counts a
// storefront publishes on its selling page (handlers.BloemLibraryFiguresHandler).
func registerBloemLibraryFiguresDocument(reg *Registry) {
	op := bloemChiDocumentOp(reg, "GET", "/admin/platform/library-figures", "getBloemLibraryFigures", "libraries", "Read item counts for every library.", "bloemPlatformContext", http.StatusOK)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = "Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. Counts every library, enabled or not, in one read-only snapshot: no titles, paths or accounts. A full count of a large catalog takes about a second, so cache the result rather than poll. Responses are not cacheable."
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "invalid_request: query parameters were sent.",
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled.",
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route.",
		503: "library_figures_unavailable: the database is unavailable or the count timed out; tenant_unavailable or service_unavailable: the credential could not be checked.",
	})
	op.Responses["200"] = &huma.Response{
		Description: http.StatusText(http.StatusOK),
		Headers:     map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}},
	}
	registerBloemChiDocument[struct{}, BloemLibraryFiguresOutput](reg, op)
}
