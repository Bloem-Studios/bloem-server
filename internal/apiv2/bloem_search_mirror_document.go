package apiv2

import (
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	catalogsvc "github.com/Silo-Server/silo-server/internal/catalog"
)

// BloemSearchMirrorStatusOutput is the search mirror's status.
type BloemSearchMirrorStatusOutput struct{ Body catalogsvc.SearchMirrorStatus }

// registerBloemSearchMirrorDocument documents the search mirror's read-only status
// (handlers.BloemSearchMirrorHandler).
func registerBloemSearchMirrorDocument(reg *Registry) {
	op := bloemChiDocumentOp(reg, "GET", "/admin/search/mirror", "getBloemSearchMirrorStatus", "system", "Read the search mirror's status.", "bloemPlatformContext", http.StatusOK)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = "Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. " +
		"The search mirror is an optional second Meilisearch that holds a copy of the catalog index, switched on by SILO_MEILISEARCH_MIRROR_URL. " +
		"Reports whether this server has it configured (its address without any password, and where its key comes from, never the key), its state " +
		"(off, following, copying, waiting, unreachable, needs_full_copy, misconfigured, failing or settling), how far behind it is, the rows waiting, " +
		"the published index next to the main index, the last full copy, the last error, the dead-letter count and the lag cap. " +
		"state_exists is true when this database holds mirror state even though this server has no mirror configured; warnings then carries configured_on_one_copy_only. " +
		"The route is read-only. Responses are not cacheable."
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled.",
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key; forbidden: the API key's scopes exclude this route.",
		503: "search_mirror_status_unavailable: the database is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked.",
	})
	op.Responses["200"] = &huma.Response{
		Description: http.StatusText(http.StatusOK),
		Headers:     map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}},
	}
	registerBloemChiDocument[struct{}, BloemSearchMirrorStatusOutput](reg, op)

	// The handler writes copy as null when no copy is running, and Huma cannot put a
	// nullable tag on an object reference: make the schema say so.
	status := reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[catalogsvc.SearchMirrorStatus](), false, "")
	copied := *status.Properties["copy"]
	description := copied.Description
	copied.Description = ""
	status.Properties["copy"] = &huma.Schema{
		Description: description,
		AnyOf:       []*huma.Schema{&copied, {Type: "null"}},
	}
}
