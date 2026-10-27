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
		op.Description = "Starts at the retained configured source root. Directory references and cursors are opaque host evidence bound to source/configuration/generation, actor, scope and expiry; a cursor is used only with its directory_ref. Requests have no body; repeated, empty and unknown query fields are rejected. Combined encoded query is limited to 6144 bytes. At most 100 directories per response. Inspect native-storage library_location_browsing first. Audio providers must explicitly support directory-only listing; browsing does not establish audio readiness."
		libraryLocationErrors(reg, &op, "BROWSE")
		registerBloemChiDocument[BloemSourceBrowseInput, BloemSourceBrowseOutput](reg, op)
	}
}
func libraryLocationDocument[T any](reg *Registry, scope, method, path, id, security string, status int, body *huma.RequestBody) {
	op := bloemChiDocumentOp(reg, method, path, id, "library-locations", "Read or atomically save filesystem paths and selected plugin directories.", security, status)
	op.Description = "Requires matching signed administrative context and current actor/library/source authority. Inspect native-storage library_location_atomic_editing first. POST reuses ordinary library validation/defaults and atomically saves paths and initial selections; library.paths and selections must both be present and nonnull. Library options reject legacy storage_source. At least one filesystem path or selection is required. PUT replaces paths and selections only; both arrays must be present and nonnull, with at most 128 roots from 16 plugin sources. GET returns every retained location/root plus active selections, revision and an expiring edit_token. Disabled or unavailable roots remain visible for removal; selections on a disabled source have an empty selection_ref. PUT requires expected_revision and expected_edit_token from GET. Tokens bind actor, library, owned revision and path row identities/xmin, so a legacy path write that leaves path rows (including A-to-B-to-A) requires reload. A path set that is empty when read and empty again when saved has no row evidence; that save replaces exactly the empty set that was read. A successful replacement that changes filesystem paths queues the ordinary library_paths_changed scan. Until native-storage library_location_mixing is true, a library keeps filesystem paths or one plugin source over its lifetime, counting retained locations; other shapes return location_mixing_unavailable. Removal retains root/location/catalog identities and disables publication memberships without deleting provider files. Provider I/O occurs before write transactions. mutation_outcome_unknown requires reconciliation using the returned library_id, never automatic replay. Plugin selections are admitted for ebook libraries, and for movie and audiobook libraries only while native-storage native_movie_libraries or native_audio_libraries is true; each selected source must declare the library's media kind."
	op.RequestBody = body
	if method != "POST" {
		one := float64(1)
		op.Parameters = append(op.Parameters, &huma.Param{Name: "library_id", In: "path", Required: true, Schema: &huma.Schema{Type: "integer", Minimum: &one}})
	}
	libraryLocationErrors(reg, &op, method)
	registerBloemChiDocument[struct{}, bloemNativeStorageOutput[T]](reg, op)
}

// libraryLocationErrors documents what each handler and its domain calls can
// write. kind is the HTTP method of a library-location operation, or BROWSE
// for the source directory browser, which shares the library-location domain.
func libraryLocationErrors(reg *Registry, op *huma.Operation, kind string) {
	const (
		unavailable = "native_storage_unavailable: library-location editing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked."
		selections  = "invalid_selection_reference: a selection_ref is invalid, expired or no longer matches its source; browse again. revision_conflict with current_source_revision: a selection's configuration_revision is stale. conflict: another library already uses a requested filesystem path."
		limits      = "selection_limit_exceeded: at most 128 selected roots and 16 plugin sources; location_mixing_unavailable: filesystem paths with plugin sources, or several plugin sources, before library_location_mixing."
		mutation    = " mutation_outcome_unknown returns operation_id, operation and library_id: reconcile using the library_id, never assume rollback or retry."
	)
	bloemDocumentErrors[BloemNativeError](reg, op, map[int]string{403: bloemNativeStorageForbidden})
	responses := map[int]string{404: "not_found: an absent or hidden library or source, or an invalid library_id; hidden resources retain not_found precedence."}
	switch kind {
	case "POST":
		responses[400] = "invalid_request: malformed JSON, unknown, duplicate or missing fields, no path or selection, organization_id in organization scope, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: ordinary library validation failed, including legacy storage_source, blank paths, metadata_language and artwork-dependent options."
		responses[404] = "not_found: a selected source, or the owner named by organization_id, is absent or hidden; hidden resources retain not_found precedence."
		responses[409] = selections
		responses[413] = "request_too_large: JSON body exceeds 1 MiB."
		responses[422] = limits
		responses[500] = "internal_error: the library could not be created."
		responses[503] = unavailable + " A plugin selection for a library type the host does not admit is also native_storage_unavailable." + mutation
	case "GET":
		responses[400] = "invalid_request: query parameters or a request body were sent."
		responses[409] = "revision_conflict: a concurrent filesystem path write holds the library's paths; returns current_library_revision. Retry the read."
		responses[503] = unavailable
	case "PUT":
		responses[400] = "invalid_request: malformed JSON, unknown, duplicate or missing fields, absent or null paths or selections, both empty, a nonpositive expected_revision, an empty expected_edit_token, or a selected source that does not declare the library's media kind. duplicate_selection: the same directory is selected twice. bad_request: a blank filesystem path."
		responses[409] = "revision_conflict with current_library_revision: expected_revision or expected_edit_token is stale, or a concurrent path write holds the library's paths; reload. " + selections
		responses[413] = "request_too_large: JSON body exceeds 1 MiB."
		responses[422] = limits
		responses[503] = unavailable + " A plugin selection for a library type the host does not admit is also native_storage_unavailable." + mutation
	case "BROWSE":
		responses[400] = "invalid_request: unknown, repeated or empty query parameters, a request body, a limit outside 1-100, a cursor without directory_ref, or references too long for the query bound."
		responses[404] = "not_found: an invalid source_key or an absent or hidden source; hidden resources retain not_found precedence."
		responses[409] = "invalid_selection_reference: directory_ref is invalid, expired or no longer matches the source; browse_restart_required: the cursor's pagination changed or expired; restart browsing."
		responses[422] = "browse_limit_exceeded: the encoded query exceeds 6144 bytes, or the directory depth, page count or continuation bound is exceeded."
		responses[502] = "invalid_provider_page: the provider returned an invalid directory page."
		responses[503] = "native_storage_unavailable: browsing is not composed, or the source, its provider or the database is unavailable. tenant_unavailable: administrative authority could not be checked."
	}
	bloemDocumentErrors[BloemNativeStorageError](reg, op, responses)
	// The handlers write every response, success included, with no-store.
	op.Responses[strconv.Itoa(op.DefaultStatus)] = &huma.Response{Description: http.StatusText(op.DefaultStatus)}
	for _, response := range op.Responses {
		response.Headers = map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}
	}
}
