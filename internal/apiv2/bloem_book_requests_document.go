package apiv2

import "github.com/Silo-Server/silo-server/internal/bookrequests"

type BloemBookRequestViewerInput struct {
	ProfileID    string `header:"X-Profile-Id" required:"true" doc:"Verified profile in the authenticated account's current organization."`
	ProfileToken string `header:"X-Profile-Token" doc:"PIN verification when required."`
}
type BloemBookRequestListInput struct {
	BloemBookRequestViewerInput
	Limit  int `query:"limit" default:"100" maximum:"100" minimum:"1"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}
type BloemBookRequestCreateInput struct {
	BloemBookRequestViewerInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"16" maxLength:"128" doc:"A durable key scoped to organization, account and profile. Reuse only for the same normalized request."`
	Body           bookrequests.CreateInput
}
type BloemBookRequestGetInput struct {
	BloemBookRequestViewerInput
	ID string `path:"id" format:"uuid"`
}
type BloemBookRequestCancelInput struct {
	BloemBookRequestGetInput
	Body bookrequests.ActionInput
}
type BloemBookRequestActionInput struct {
	ID   string `path:"id" format:"uuid"`
	Body bookrequests.ActionInput
}
type BloemBookRequestAdminListInput struct {
	Limit  int `query:"limit" default:"100" maximum:"100" minimum:"1"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}
type BloemBookRequestOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.Request
}
type BloemBookRequestListOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.ListResponse
}
type BloemBookRequestCapabilityOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.Capability
}

func registerBloemBookRequestsDocument(reg *Registry) {
	registerBloemEbookRequestsDocument(reg)
	registerBloemAudiobookAcquisitionDocument(reg)
	//nolint:misspell // awaiting_fulfilment is the established wire enum.
	const description = "Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while the backend is unconfigured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability."
	errors := map[int]string{400: "invalid_request: invalid input, JSON, identity, Idempotency-Key or expected_revision.", 404: "not_found: request absent or outside this profile/organization.", 409: "already_requested or request_state_changed: a duplicate active identity, changed key payload, stale revision or invalid transition.", 429: "request_quota_exceeded: the independent audiobook quota is exhausted, or rate_limit_exceeded."}
	capability := bloemChiDocumentOp(reg, "GET", "/book-requests/capability", "getBloemBookRequestCapability", "requests", "Read audiobook request capability and current viewer policy.", "bearerAuth", 200)
	capability.Description = description
	registerBloemChiDocument[BloemBookRequestViewerInput, BloemBookRequestCapabilityOutput](reg, capability)
	list := bloemChiDocumentOp(reg, "GET", "/book-requests/mine", "listBloemBookRequests", "requests", "List this profile's audiobook requests in the current organization.", "bearerAuth", 200)
	list.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &list, errors)
	registerBloemChiDocument[BloemBookRequestListInput, BloemBookRequestListOutput](reg, list)
	create := bloemChiDocumentOp(reg, "POST", "/book-requests", "createBloemBookRequest", "requests", "File an audiobook request.", "bearerAuth", 201)
	create.Description = description + " ISBN-10 is normalized to ISBN-13; ASIN is uppercased. A title plus author can be filed without identifiers and can be fulfilled manually. An identical Idempotency-Key replay returns HTTP 200 with the existing request, including after completion or withdrawal; changed normalized input returns 409. A different key for an active identity returns 409 without revealing another profile's request. Unknown JSON fields and extra bodies are rejected."
	bloemDocumentErrors[BloemNativeError](reg, &create, errors)
	registerBloemChiDocument[BloemBookRequestCreateInput, BloemBookRequestOutput](reg, create)
	// The replay response has the same body schema as the created response.
	registeredCreate := reg.api.OpenAPI().Paths[create.Path].Post
	registeredCreate.Responses["200"] = registeredCreate.Responses["201"]
	get := bloemChiDocumentOp(reg, "GET", "/book-requests/{id}", "getBloemBookRequest", "requests", "Read this profile's audiobook request.", "bearerAuth", 200)
	get.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &get, errors)
	registerBloemChiDocument[BloemBookRequestGetInput, BloemBookRequestOutput](reg, get)
	cancel := bloemChiDocumentOp(reg, "POST", "/book-requests/{id}/cancel", "cancelBloemBookRequest", "requests", "Withdraw a pending or approved audiobook request.", "bearerAuth", 200)
	cancel.Description = description + " expected_revision must match the current row. Withdrawal retains its quota usage."
	bloemDocumentErrors[BloemNativeError](reg, &cancel, errors)
	registerBloemChiDocument[BloemBookRequestCancelInput, BloemBookRequestOutput](reg, cancel)
	adminList := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests", "listBloemAdminBookRequests", "requests", "List audiobook requests in the selected administrative organization.", "adminContextAuth", 200)
	adminList.Description = "Requires an organization administrative context. Platform-only contexts do not grant organization queue access. Pagination is applied after organization isolation."
	bloemDocumentErrors[BloemNativeError](reg, &adminList, errors)
	registerBloemChiDocument[BloemBookRequestAdminListInput, BloemBookRequestListOutput](reg, adminList)
	for _, action := range []struct{ name, id, summary string }{
		{"approve", "approveBloemBookRequest", "Approve an audiobook request; keep it awaiting fulfillment."},
		{"decline", "declineBloemBookRequest", "Decline a pending audiobook request and refund its quota slot."},
		{"fulfil", "fulfilBloemBookRequest", "Manually mark an audiobook request fulfilled."}, //nolint:misspell // Preserve the established route and operation identifiers.
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/"+action.name, action.id, "requests", action.summary, "adminContextAuth", 200)
		op.Description = "Requires current organization administrator authority and a matching expected_revision. Other organizations' requests return 404. Manual fulfilment records fulfilment=manual without claiming a playable library item." //nolint:misspell // fulfilment is the persisted wire field.
		bloemDocumentErrors[BloemNativeError](reg, &op, errors)
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}
}
