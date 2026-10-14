package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

type BloemEbookRequestCreateInput struct {
	BloemBookRequestViewerInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"16" maxLength:"128"`
	Body           struct {
		Title  string `json:"title" minLength:"1"`
		Author string `json:"author,omitempty"`
		ISBN   string `json:"isbn" minLength:"1" doc:"Valid ISBN-10 or ISBN-13; normalized to canonical ISBN-13."`
		Note   string `json:"note,omitempty"`
	}
}
type BloemEbookRequestSetupOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.EbookRequestSetup
}
type BloemEbookRequestConnectionInput struct {
	Body handlers.EbookRequestConnectionInput
}
type BloemEbookRequestIntegrationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         bookrequests.EbookIntegration
}
type BloemEbookRequestLibraryInput struct {
	ID   string `path:"id" doc:"Opaque request integration ID, often a decimal Sonyflake string."`
	Body struct {
		LibraryID int `json:"library_id" minimum:"0" doc:"Eligible native Bookwarehouse ebook library; zero removes the binding."`
	}
}
type BloemEbookRequestPolicyInput struct{ Body handlers.EbookRequestPolicy }
type BloemEbookRequestPolicyOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         handlers.EbookRequestPolicy
}

func registerBloemEbookRequestsDocument(reg *Registry) {
	const description = "Ebook requests are default-off, web/admin first, and limited to the durable default organization. Enabled accounts, active membership and a verified current profile are required. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes."
	errors := map[int]string{400: "invalid_request: invalid body, ISBN, key or action.", 403: "request_forbidden or requests_disabled: current organization/identity/policy does not permit this operation.", 404: "not_found: absent or outside this profile/organization.", 409: "already_requested or request_state_changed: duplicate active identity, changed retry payload, stale revision or invalid transition.", 429: "request_quota_exceeded: independent ebook quota exhausted, or rate_limit_exceeded."}
	capability := bloemChiDocumentOp(reg, "GET", "/ebook-requests/capability", "getBloemEbookRequestCapability", "requests", "Read ebook request capability and current viewer policy.", "bearerAuth", 200)
	capability.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &capability, errors)
	registerBloemChiDocument[BloemBookRequestViewerInput, BloemBookRequestCapabilityOutput](reg, capability)
	list := bloemChiDocumentOp(reg, "GET", "/ebook-requests/mine", "listBloemEbookRequests", "requests", "List this profile's ebook request history.", "bearerAuth", 200)
	list.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &list, errors)
	registerBloemChiDocument[BloemBookRequestListInput, BloemBookRequestListOutput](reg, list)
	create := bloemChiDocumentOp(reg, "POST", "/ebook-requests", "createBloemEbookRequest", "requests", "File an ISBN ebook request.", "bearerAuth", 201)
	create.Description = description + " Replaying the same normalized payload with its durable Idempotency-Key returns HTTP 200; a changed payload returns 409. Unknown JSON fields, ASIN and extra bodies are rejected."
	bloemDocumentErrors[BloemNativeError](reg, &create, errors)
	registerBloemChiDocument[BloemEbookRequestCreateInput, BloemBookRequestOutput](reg, create)
	reg.api.OpenAPI().Paths[create.Path].Post.Responses["200"] = reg.api.OpenAPI().Paths[create.Path].Post.Responses["201"]
	get := bloemChiDocumentOp(reg, "GET", "/ebook-requests/{id}", "getBloemEbookRequest", "requests", "Read an ebook request owned by this profile.", "bearerAuth", 200)
	get.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &get, errors)
	registerBloemChiDocument[BloemBookRequestGetInput, BloemBookRequestOutput](reg, get)
	cancel := bloemChiDocumentOp(reg, "POST", "/ebook-requests/{id}/cancel", "cancelBloemEbookRequest", "requests", "Withdraw a pending or approved ebook request locally.", "bearerAuth", 200)
	cancel.Description = description + " A matching expected_revision is required; quota usage is retained."
	bloemDocumentErrors[BloemNativeError](reg, &cancel, errors)
	registerBloemChiDocument[BloemBookRequestCancelInput, BloemBookRequestOutput](reg, cancel)
	admin := bloemChiDocumentOp(reg, "GET", "/admin/organization/ebook-requests", "listBloemAdminEbookRequests", "requests", "List ebook requests in the selected default organization.", "adminContextAuth", 200)
	admin.Description = description
	bloemDocumentErrors[BloemNativeError](reg, &admin, errors)
	registerBloemChiDocument[BloemBookRequestAdminListInput, BloemBookRequestListOutput](reg, admin)
	for _, action := range []struct{ name, id, summary string }{
		{"approve", "approveBloemEbookRequest", "Approve a pending ebook request."},
		{"decline", "declineBloemEbookRequest", "Decline a pending ebook request and refund its quota slot."},
		{"retry", "retryBloemEbookRequest", "Recover uncertain provider state or retry a safely failed ebook dispatch."},
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-requests/{id}/"+action.name, action.id, "requests", action.summary, "adminContextAuth", 200)
		op.Description = description + " Current organization administrator authority and a matching expected_revision are required. Uncertain submissions are recovered through read-only provider checks; they are never blindly resubmitted. Manual ebook completion is unavailable."
		bloemDocumentErrors[BloemNativeError](reg, &op, errors)
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}
	const authority = "Mutations require platform administrator authority within an organization administrative context for the durable default organization. Platform-only contexts and hosted organizations cannot configure this lane. Connection credentials are encrypted by the host and never included in responses."
	setup := bloemChiDocumentOp(reg, "GET", "/admin/organization/ebook-request-integrations", "getBloemEbookRequestSetup", "requests", "Read ebook policy, connections and eligible libraries/installations.", "adminContextAuth", 200)
	setup.Description = authority + " Hosted organizations receive available=false and empty configuration lists."
	bloemDocumentErrors[BloemNativeError](reg, &setup, errors)
	registerBloemChiDocument[struct{}, BloemEbookRequestSetupOutput](reg, setup)
	connection := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-request-integrations", "createBloemEbookRequestIntegration", "requests", "Create an explicit Bookwarehouse ebook fulfillment connection.", "adminContextAuth", 201)
	connection.Description = authority + " Supported media types are fixed to book and EPUB is the default. HTTP requires explicit allow_http for a trusted local endpoint. Native storage installations cannot serve this capability."
	bloemDocumentErrors[BloemNativeError](reg, &connection, errors)
	registerBloemChiDocument[BloemEbookRequestConnectionInput, BloemEbookRequestIntegrationOutput](reg, connection)
	binding := bloemChiDocumentOp(reg, "PUT", "/admin/organization/ebook-request-integrations/{id}/library", "setBloemEbookRequestIntegrationLibrary", "requests", "Bind a connection to an eligible native Bookwarehouse ebook library.", "adminContextAuth", 200)
	binding.Description = authority + " library_id=0 clears the binding. Existing dispatched work retains its original library and configuration identity."
	bloemDocumentErrors[BloemNativeError](reg, &binding, errors)
	registerBloemChiDocument[BloemEbookRequestLibraryInput, BloemEbookRequestIntegrationOutput](reg, binding)
	test := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-request-integrations/{id}/test", "testBloemEbookRequestIntegration", "requests", "Run the Bookwarehouse plugin's connection test for a saved connection.", "adminContextAuth", 200)
	test.Description = authority + " The request has no body. A failed test is HTTP 200 with ok=false. A plugin message that repeats the saved API key is withheld."
	bloemDocumentErrors[BloemNativeError](reg, &test, errors)
	registerBloemChiDocument[BloemRequestIntegrationTestInput, BloemRequestIntegrationTestOutput](reg, test)
	policy := bloemChiDocumentOp(reg, "PUT", "/admin/organization/ebook-request-policy", "setBloemEbookRequestPolicy", "requests", "Explicitly enable or disable ebook request admission and dispatch.", "adminContextAuth", 200)
	policy.Description = authority + " The default is false. Disabling prevents new requests and new provider work; work already submitted may continue at Bookwarehouse. History and local cancellation remain available."
	bloemDocumentErrors[BloemNativeError](reg, &policy, errors)
	registerBloemChiDocument[BloemEbookRequestPolicyInput, BloemEbookRequestPolicyOutput](reg, policy)
}
