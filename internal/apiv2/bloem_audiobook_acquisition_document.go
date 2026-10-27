package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

type BloemAudiobookAcquisitionListInput struct {
	State  string `query:"state" enum:"all,active,review,failed,stuck" doc:"Acquisition state filter; omitted means all."`
	Cursor string `query:"cursor" doc:"Opaque next_cursor from the previous page."`
	Limit  int    `query:"limit" default:"100" minimum:"1" maximum:"100"`
}
type BloemAudiobookAcquisitionGetInput struct {
	ID string `path:"id" format:"uuid"`
}
type BloemAudiobookCandidateApprovalInput struct {
	ID   string `path:"id" format:"uuid"`
	Body bookrequests.AudiobookCandidateApproval
}
type BloemAudiobookAcquisitionOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.AudiobookAcquisition
}
type BloemAudiobookAcquisitionListOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.AudiobookAcquisitionList
}
type BloemAudiobookRequestSetupOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.AudiobookRequestSetup
}
type BloemAudiobookRequestConnectionInput struct {
	Body handlers.AudiobookRequestConnectionInput
}
type BloemAudiobookRequestIntegrationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.AudiobookIntegrationSummary
}
type BloemAudiobookRequestUpdateInput struct {
	ID   string `path:"id" doc:"Opaque request integration ID, often a decimal Sonyflake string."`
	Body handlers.AudiobookRequestConnectionUpdate
}
type BloemRequestIntegrationTestInput struct {
	ID string `path:"id" doc:"Opaque request integration ID, often a decimal Sonyflake string."`
}
type BloemRequestIntegrationTestOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.RequestIntegrationTestResult
}
type BloemAudiobookRequestLibraryInput struct {
	ID   string `path:"id" doc:"Opaque request integration ID, often a decimal Sonyflake string."`
	Body struct {
		LibraryID     int      `json:"library_id" minimum:"0" doc:"Eligible audiobook library; zero clears the binding."`
		DownloadRoots []string `json:"download_roots" doc:"Local download directories within the selected library's filesystem paths."`
	}
}

// Document the existing administrative chi routes without mounting new handlers.
func registerBloemAudiobookAcquisitionDocument(reg *Registry) {
	const authority = "Requires an organization administrative-context token and current administrator authority in the selected organization. Platform-only contexts do not grant organization queue access. Acquisition details are not exposed to ordinary requesters. Responses use Cache-Control: private, no-store."
	const memberForbidden = "request_forbidden: the account or its membership in the organization no longer qualifies."
	const conflictBase = "request_state_changed: expected_revision is stale, the request is not active and approved"
	list := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests/acquisitions", "listBloemAudiobookAcquisitions", "requests", "List audiobook acquisitions by state in the selected organization.", bloemOrganizationContextSecurity, 200)
	list.Description = authority + " Results are ordered by newest change; next_cursor resumes the listing."
	bloemRequestDocErrors(reg, &list, bloemRequestAdminErrors(map[int]string{
		400: "invalid_request: limit outside 1 to 100, an unknown state, or a cursor that is not an unmodified next_cursor.",
		403: memberForbidden,
	}))
	registerBloemChiDocument[BloemAudiobookAcquisitionListInput, BloemAudiobookAcquisitionListOutput](reg, list)
	get := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests/{id}/acquisition", "getBloemAudiobookAcquisition", "requests", "Read an audiobook acquisition and its recent decisions.", bloemOrganizationContextSecurity, 200)
	get.Description = authority + " Includes at most the newest 20 stored decisions."
	bloemRequestDocErrors(reg, &get, bloemRequestAdminErrors(map[int]string{403: memberForbidden, 404: bloemRequestNotFound("an audiobook request")}))
	registerBloemChiDocument[BloemAudiobookAcquisitionGetInput, BloemAudiobookAcquisitionOutput](reg, get)
	approval := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/approve-candidate", "approveBloemAudiobookCandidate", "requests", "Approve a held audiobook candidate for a new acquisition attempt.", bloemOrganizationContextSecurity, 200)
	approval.Description = authority + " expected_revision must match the current request. candidate_ref must identify a candidate in the newest held selection decision of the current attempt; a malformed reference, including an arbitrary URL, returns 400 and a well-formed reference outside that decision returns 409. Unknown JSON fields and extra bodies are rejected."
	bloemRequestDocErrors(reg, &approval, bloemRequestAdminErrors(map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + ", a malformed candidate_ref, or expected_revision missing or not positive.",
		403: memberForbidden,
		404: bloemRequestNotFound("an audiobook request"),
		409: conflictBase + ", it is not held for selection review, the candidate is not in the newest held selection decision, its connection was removed or replaced, or the attempt limit was reached.",
	}))
	registerBloemChiDocument[BloemAudiobookCandidateApprovalInput, BloemBookRequestOutput](reg, approval)
	for _, action := range []struct{ name, id, summary, description, forbidden, conflict string }{
		{"accept-download", "acceptBloemAudiobookDownload", "Accept the selected audiobook download for import.", " Accepts a download held for verification; ambiguous multiple-item folders are refused. This does not mark the request fulfilled. A matching item must still be scanned into the retained library.", memberForbidden, ", it is not held for verification review, or verification found multiple items."},
		{"retry", "retryBloemAudiobookAcquisition", "Retry or recover an audiobook acquisition.", " Recovery retains the existing dispatch identity; uncertain downloads are checked instead of blindly submitted again. A failed, held or stuck acquisition starts a new attempt; a retrying one is checked again. reason is accepted but replaced by the server's requester-safe reason.", "", ", it has no acquisition dispatch, its connection was removed or replaced, nothing is failed, held, stuck or retrying, or the attempt limit was reached."},
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/"+action.name, action.id, "requests", action.summary, bloemOrganizationContextSecurity, 200)
		op.Description = authority + " expected_revision must match the current request. Unknown JSON fields and extra bodies are rejected." + action.description
		errors := map[int]string{
			400: "invalid_request: " + bloemRequestActionBodyRule + ".",
			404: bloemRequestNotFound("an audiobook request"),
			409: conflictBase + action.conflict,
		}
		if action.forbidden != "" {
			errors[403] = action.forbidden
		}
		bloemRequestDocErrors(reg, &op, bloemRequestAdminErrors(errors))
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}
	const setupAuthority = "Requires an organization administrative-context token. Every operation except reading the setup requires platform administrator authority (platform_admin effective authority in the context). Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store."
	const platformForbidden = "request_forbidden: the context lacks platform_admin effective authority, the account is no longer a platform administrator, or its membership no longer qualifies."
	setupErrors := func(extra map[int]string) map[int]string {
		errors := bloemRequestAdminErrors(extra)
		errors[500] = "internal_error: the request store, platform authority check or plugin host failed."
		errors[503] = "tenant_unavailable: administrative authority could not be checked; unavailable: audiobook request setup is not configured."
		return errors
	}
	setup := bloemChiDocumentOp(reg, "GET", "/admin/organization/audiobook-request-integrations", "getBloemAudiobookRequestSetup", "requests", "Read audiobook acquisition connections and eligible libraries/installations.", bloemOrganizationContextSecurity, 200)
	setup.Description = setupAuthority + " Each connection's saved plugin_config and the names of its saved credentials (stored_credentials) are returned only to a platform administrator context; an absent plugin_config then means the saved settings could not be read. Organization administrators receive each connection's identity, binding and readiness."
	bloemRequestDocErrors(reg, &setup, setupErrors(map[int]string{403: memberForbidden}))
	registerBloemChiDocument[struct{}, BloemAudiobookRequestSetupOutput](reg, setup)
	create := bloemChiDocumentOp(reg, "POST", "/admin/organization/audiobook-request-integrations", "createBloemAudiobookRequestIntegration", "requests", "Create an audiobook acquisition connection.", bloemOrganizationContextSecurity, 201)
	create.Description = setupAuthority + " The installation and capability must be an eligible ordinary audiobook request plugin. Supported media types are fixed to audiobook. Unknown JSON fields and extra bodies are rejected."
	bloemRequestDocErrors(reg, &create, setupErrors(map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + "; a name missing or over 500 bytes, installation_id and capability_id not naming an eligible installation, a plugin_config carrying service_kind, or incomplete or invalid credentials.",
		403: platformForbidden,
	}))
	registerBloemChiDocument[BloemAudiobookRequestConnectionInput, BloemAudiobookRequestIntegrationOutput](reg, create)
	update := bloemChiDocumentOp(reg, "PUT", "/admin/organization/audiobook-request-integrations/{id}", "updateBloemAudiobookRequestIntegration", "requests", "Edit an audiobook acquisition connection and rotate its credentials.", bloemOrganizationContextSecurity, 200)
	update.Description = setupAuthority + " The connection keeps its ID, library binding and in-flight acquisitions. plugin_config is merged over the saved settings: a submitted key replaces its saved value, null removes it, and an omitted key keeps it. A credential that is omitted or empty keeps its saved value only while the URL it is bound to is unchanged; a changed URL requires the credential again, and a URL cleared to an empty string or removed with null drops it. Changing qbittorrent_url or nzbget_url makes downloads already handed to the previous client fail their status checks. Silo's generic request-integration editor refuses audiobook connections. Unknown JSON fields and extra bodies are rejected."
	bloemRequestDocErrors(reg, &update, setupErrors(map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + "; a name over 500 bytes, a plugin_config carrying service_kind, a credential missing after its URL changed, or settings the plugin rejected (its validation text is included with credentials withheld).",
		403: platformForbidden,
		404: "not_found: the connection is absent or not usable by this organization's audiobook lane.",
		409: "request_state_changed: the connection changed concurrently, or its saved installation or capability no longer matches the eligible connection.",
	}))
	registerBloemChiDocument[BloemAudiobookRequestUpdateInput, BloemAudiobookRequestIntegrationOutput](reg, update)
	test := bloemChiDocumentOp(reg, "POST", "/admin/organization/audiobook-request-integrations/{id}/test", "testBloemAudiobookRequestIntegration", "requests", "Run the audiobook plugin's connection checks for a saved connection.", bloemOrganizationContextSecurity, 200)
	test.Description = setupAuthority + " The request has no body. A failed check is HTTP 200 with ok=false. When a saved credential can no longer be read or no longer matches its URL, the host answers ok=false without calling the plugin; it also withholds a plugin message that repeats a credential."
	bloemRequestDocErrors(reg, &test, setupErrors(map[int]string{
		403: platformForbidden,
		404: "not_found: the connection is absent or not usable by this organization's audiobook lane.",
		409: "request_state_changed: the saved connection's installation or capability no longer matches the eligible connection.",
	}))
	registerBloemChiDocument[BloemRequestIntegrationTestInput, BloemRequestIntegrationTestOutput](reg, test)
	binding := bloemChiDocumentOp(reg, "PUT", "/admin/organization/audiobook-request-integrations/{id}/library", "setBloemAudiobookRequestIntegrationLibrary", "requests", "Bind an acquisition connection to a local audiobook library and download directories.", bloemOrganizationContextSecurity, 200)
	binding.Description = setupAuthority + " library_id=0 clears the binding. Otherwise one to four download roots are required: clean absolute directories inside the selected library's filesystem paths, distinct, not nested, and each holding at most 10000 catalogued files. Existing dispatched work retains its original library, roots and configuration identity. Unknown JSON fields and extra bodies are rejected."
	bloemRequestDocErrors(reg, &binding, setupErrors(map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + "; a negative library_id, an ID that is not an eligible connection, a library that is not an eligible audiobook library of this organization, invalid download roots, or a connection bound by another organization.",
		403: platformForbidden,
	}))
	registerBloemChiDocument[BloemAudiobookRequestLibraryInput, BloemAudiobookRequestIntegrationOutput](reg, binding)
}
