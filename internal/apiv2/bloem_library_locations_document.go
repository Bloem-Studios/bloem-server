package apiv2

import (
	"net/http"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/danielgtaylor/huma/v2"
)

type BloemOrganizationLibraryLocationsCreate struct {
	Library    handlers.BloemLibraryCreateOptions `json:"library"`
	Selections []nativestorage.RootSelection      `json:"selections" nullable:"false" maxItems:"128"`
}

// The runtime platform request is reused; organization scope rejects its
// organization_id field, including null, and owns libraries in its context.
func registerBloemLibraryLocationsDocument(reg *Registry) {
	for _, scope := range []string{"platform", "organization"} {
		security, label := bloemPlatformContextSecurity, "Platform"
		if scope == "organization" {
			security, label = bloemOrganizationContextSecurity, "Organization"
		}
		base := "/admin/" + scope + "/library-locations"
		create := bloemNativeStorageJSONBody[handlers.BloemLibraryLocationsCreateRequest](reg)
		if scope == "organization" {
			create = bloemNativeStorageJSONBody[BloemOrganizationLibraryLocationsCreate](reg)
		}
		libraryLocationDocument[handlers.BloemLibraryLocationsCreated](reg, scope, "POST", base, "createBloem"+label+"LibraryLocations", security, 201, create)
		libraryLocationDocument[nativestorage.LibrarySelection](reg, scope, "GET", base+"/{library_id}", "getBloem"+label+"LibraryLocations", security, 200, nil)
		libraryLocationDocument[nativestorage.LibrarySelection](reg, scope, "PUT", base+"/{library_id}", "replaceBloem"+label+"LibraryLocations", security, 200, bloemNativeStorageJSONBody[nativestorage.ReplaceLocationsRequest](reg))
		op := bloemChiDocumentOp(reg, "GET", "/admin/"+scope+"/native-storage/sources/{source_key}/browse", "browseBloem"+label+"NativeStorage", "native-storage", "Browse authorized directories without starting discovery or opening files.", security, 200)
		op.Description = "Starts at the retained configured source root. Directory references and cursors are opaque host evidence bound to source/configuration/generation, actor, scope and expiry. Requests have no body; repeated, empty and unknown query fields are rejected. Combined encoded query is limited to 6144 bytes. At most 100 directories per response. Audio providers must explicitly support directory-only listing; browsing does not establish audio readiness."
		libraryLocationErrors(reg, &op)
		registerBloemChiDocument[BloemSourceBrowseInput, BloemSourceBrowseOutput](reg, op)
	}
}
func libraryLocationDocument[T any](reg *Registry, scope, method, path, id, security string, status int, body *huma.RequestBody) {
	op := bloemChiDocumentOp(reg, method, path, id, "library-locations", "Read or atomically save filesystem paths and selected plugin directories.", security, status)
	op.Description = "Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal and may have no selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A conflict returns current_library_revision. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. These mutations are outside shared lifecycle idempotency: mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Native audio remains unavailable until full scanner/catalog/playback composition."
	op.RequestBody = body
	if method != "POST" {
		one := float64(1)
		op.Parameters = append(op.Parameters, &huma.Param{Name: "library_id", In: "path", Required: true, Schema: &huma.Schema{Type: "integer", Minimum: &one}})
	}
	libraryLocationErrors(reg, &op)
	registerBloemChiDocument[struct{}, bloemNativeStorageOutput[T]](reg, op)
}
func libraryLocationErrors(reg *Registry, op *huma.Operation) {
	bloemDocumentErrors[BloemNativeStorageError](reg, op, map[int]string{
		400: "invalid_request, duplicate_selection or ordinary library bad_request: invalid, unknown, duplicate or missing fields.",
		404: "not_found: absent or hidden resource retains precedence.",
		409: "revision_conflict returns current_library_revision and requires reloading; invalid_selection_reference or browse_restart_required requires browsing again. Ordinary duplicate paths return conflict.",
		413: "request_too_large: JSON body exceeds 1 MiB.",
		422: "selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; browse_limit_exceeded: bounded query/pagination limit exceeded; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing.",
		502: "invalid_provider_page: invalid bounded directory response.",
		503: "native_storage_unavailable or mutation_outcome_unknown; reconcile authorized recovery identifiers, never assume rollback or retry.",
	})
	// The handlers write every response, success included, with no-store.
	op.Responses[strconv.Itoa(op.DefaultStatus)] = &huma.Response{Description: http.StatusText(op.DefaultStatus)}
	for _, response := range op.Responses {
		response.Headers = map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	}
}
