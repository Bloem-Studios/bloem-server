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
	errors := map[int]string{
		400: "invalid_request: invalid JSON, request identity, query, expected_revision, candidate or integration binding.",
		403: "request_forbidden or requests_disabled: current organization or authority does not permit this operation.",
		404: "not_found: request absent or outside the selected organization.",
		409: "request_state_changed: stale expected_revision, invalid transition or changed acquisition identity.",
		500: "internal_error: the acquisition operation could not be completed.",
	}
	list := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests/acquisitions", "listBloemAudiobookAcquisitions", "requests", "List audiobook acquisitions by state in the selected organization.", "adminContextAuth", 200)
	list.Description = authority + " Results are ordered by newest change; next_cursor resumes the listing."
	bloemDocumentErrors[BloemNativeError](reg, &list, errors)
	registerBloemChiDocument[BloemAudiobookAcquisitionListInput, BloemAudiobookAcquisitionListOutput](reg, list)
	get := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests/{id}/acquisition", "getBloemAudiobookAcquisition", "requests", "Read an audiobook acquisition and its recent decisions.", "adminContextAuth", 200)
	get.Description = authority + " Includes at most the newest 20 stored decisions."
	bloemDocumentErrors[BloemNativeError](reg, &get, errors)
	registerBloemChiDocument[BloemAudiobookAcquisitionGetInput, BloemAudiobookAcquisitionOutput](reg, get)
	approval := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/approve-candidate", "approveBloemAudiobookCandidate", "requests", "Approve a held audiobook candidate for a new acquisition attempt.", "adminContextAuth", 200)
	approval.Description = authority + " expected_revision must match the current request. candidate_ref must identify a candidate in the newest held selection decision of the current attempt; arbitrary URLs are refused. Unknown JSON fields and extra bodies are rejected."
	bloemDocumentErrors[BloemNativeError](reg, &approval, errors)
	registerBloemChiDocument[BloemAudiobookCandidateApprovalInput, BloemBookRequestOutput](reg, approval)
	for _, action := range []struct{ name, id, summary, description string }{
		{"accept-download", "acceptBloemAudiobookDownload", "Accept the selected audiobook download for import.", " Accepts a download held for verification; ambiguous multiple-item folders are refused. This does not mark the request fulfilled. A matching item must still be scanned into the retained library."},
		{"retry", "retryBloemAudiobookAcquisition", "Retry or recover an audiobook acquisition.", " Recovery retains the existing dispatch identity; uncertain downloads are checked instead of blindly submitted again."},
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/"+action.name, action.id, "requests", action.summary, "adminContextAuth", 200)
		op.Description = authority + " expected_revision must match the current request. Unknown JSON fields and extra bodies are rejected." + action.description
		bloemDocumentErrors[BloemNativeError](reg, &op, errors)
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}
	const setupAuthority = "Requires an organization administrative-context token. Creating connections and changing bindings additionally require platform administrator authority. Credentials are encrypted by the host and never included in responses. Responses use Cache-Control: private, no-store."
	setup := bloemChiDocumentOp(reg, "GET", "/admin/organization/audiobook-request-integrations", "getBloemAudiobookRequestSetup", "requests", "Read audiobook acquisition connections and eligible libraries/installations.", "adminContextAuth", 200)
	setup.Description = setupAuthority
	bloemDocumentErrors[BloemNativeError](reg, &setup, errors)
	registerBloemChiDocument[struct{}, BloemAudiobookRequestSetupOutput](reg, setup)
	create := bloemChiDocumentOp(reg, "POST", "/admin/organization/audiobook-request-integrations", "createBloemAudiobookRequestIntegration", "requests", "Create an audiobook acquisition connection.", "adminContextAuth", 201)
	create.Description = setupAuthority + " The installation and capability must be an eligible ordinary audiobook request plugin. Supported media types are fixed to audiobook. Unknown JSON fields and extra bodies are rejected."
	bloemDocumentErrors[BloemNativeError](reg, &create, errors)
	registerBloemChiDocument[BloemAudiobookRequestConnectionInput, BloemAudiobookRequestIntegrationOutput](reg, create)
	binding := bloemChiDocumentOp(reg, "PUT", "/admin/organization/audiobook-request-integrations/{id}/library", "setBloemAudiobookRequestIntegrationLibrary", "requests", "Bind an acquisition connection to a local audiobook library and download directories.", "adminContextAuth", 200)
	binding.Description = setupAuthority + " library_id=0 clears the binding. Download roots must be valid local directories inside the selected library's filesystem paths. Existing dispatched work retains its original library, roots and configuration identity. Unknown JSON fields and extra bodies are rejected."
	bloemDocumentErrors[BloemNativeError](reg, &binding, errors)
	registerBloemChiDocument[BloemAudiobookRequestLibraryInput, BloemAudiobookRequestIntegrationOutput](reg, binding)
}
