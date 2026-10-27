package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

type BloemEbookRequestCreateInput struct {
	BloemBookRequestViewerInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"16" maxLength:"128" doc:"A durable key scoped to organization, account and profile, without spaces, tabs or line breaks. Reuse only for the same normalized request."`
	Body           struct {
		Title  string `json:"title" minLength:"1" doc:"Trimmed; at most 500 bytes."`
		Author string `json:"author,omitempty" doc:"At most 500 bytes."`
		ISBN   string `json:"isbn" minLength:"1" doc:"Valid ISBN-10 or ISBN-13; normalized to canonical ISBN-13."`
		Note   string `json:"note,omitempty" doc:"At most 2000 bytes."`
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
	const lane = "Ebook requests are default-off, web/admin first, and limited to the durable default organization. ISBN is mandatory and normalized to ISBN-13. Quotas and persistence remain separate from audiobook and TMDB requests. Authorized history and cancellation remain available after opt-out. Approval uses a separately installed ordinary Bookwarehouse request plugin and an explicit book connection linked to a native Bookwarehouse ebook library. Provider success alone never fulfills a request: a matching ebook must be scanned into the retained linked library. Cancellation is local; provider work already started may continue. Automatic audiobook acquisition is not enabled by these routes. Responses are private and no-store."
	const description = lane + " Enabled accounts, active membership and a verified current profile are required."
	const outsideDefault = "request_forbidden: the current organization is not the durable default organization, or the account, membership or profile no longer qualifies."
	const outsideDefaultAdmin = "request_forbidden: the organization is not the durable default organization, or the account or membership no longer qualifies."

	capability := bloemChiDocumentOp(reg, "GET", "/ebook-requests/capability", "getBloemEbookRequestCapability", "requests", "Read ebook request capability and current viewer policy.", "bearerAuth", 200)
	capability.Description = description + " An opted-out lane reports enabled=false and can_request=false rather than an error."
	bloemRequestDocViewer(reg, &capability, map[int]string{403: outsideDefault}, "")
	registerBloemChiDocument[BloemBookRequestViewerInput, BloemBookRequestCapabilityOutput](reg, capability)

	list := bloemChiDocumentOp(reg, "GET", "/ebook-requests/mine", "listBloemEbookRequests", "requests", "List this profile's ebook request history.", "bearerAuth", 200)
	list.Description = description + " Results are newest first."
	bloemRequestDocViewer(reg, &list, map[int]string{400: bloemRequestListQueryRule, 403: outsideDefault}, "")
	registerBloemChiDocument[BloemBookRequestListInput, BloemBookRequestListOutput](reg, list)

	create := bloemChiDocumentOp(reg, "POST", "/ebook-requests", "createBloemEbookRequest", "requests", "File an ISBN ebook request.", "bearerAuth", 201)
	create.Description = description + " Replaying the same normalized payload with its durable Idempotency-Key returns HTTP 200; a changed payload returns 409. Unknown JSON fields, ASIN and extra bodies are rejected."
	bloemRequestDocViewer(reg, &create, map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + " (asin is an unknown field here); a missing title or ISBN, an invalid ISBN, a title or author over 500 bytes, a note over 2000 bytes, or an Idempotency-Key that is not 16 to 128 characters without whitespace.",
		403: outsideDefault + " It is also returned when request policy (profile group, account limits or approval mode) does not let this profile file requests; requests_disabled: ebook requests are opted out or requests are disabled.",
		409: "already_requested: an active request for the same ISBN exists in this organization; request_state_changed: the Idempotency-Key was used with different normalized input.",
	}, "request_quota_exceeded: the independent ebook quota is exhausted (plain native error);")
	registerBloemChiDocument[BloemEbookRequestCreateInput, BloemBookRequestOutput](reg, create)
	bloemRequestDocReplay(reg, create.Path)

	get := bloemChiDocumentOp(reg, "GET", "/ebook-requests/{id}", "getBloemEbookRequest", "requests", "Read an ebook request owned by this profile.", "bearerAuth", 200)
	get.Description = description
	bloemRequestDocViewer(reg, &get, map[int]string{403: outsideDefault, 404: bloemRequestNotFound("this profile's ebook request")}, "")
	registerBloemChiDocument[BloemBookRequestGetInput, BloemBookRequestOutput](reg, get)

	cancel := bloemChiDocumentOp(reg, "POST", "/ebook-requests/{id}/cancel", "cancelBloemEbookRequest", "requests", "Withdraw a pending or approved ebook request locally.", "bearerAuth", 200)
	cancel.Description = description + " A matching expected_revision is required; quota usage is retained."
	bloemRequestDocViewer(reg, &cancel, map[int]string{
		400: "invalid_request: " + bloemRequestActionBodyRule + ".",
		403: outsideDefault,
		404: bloemRequestNotFound("this profile's ebook request"),
		409: "request_state_changed: expected_revision is stale, or the request is completed or no longer active.",
	}, "")
	registerBloemChiDocument[BloemBookRequestCancelInput, BloemBookRequestOutput](reg, cancel)

	const queue = bloemRequestOrgAdminMessage + " The organization must be the durable default organization. " + lane
	admin := bloemChiDocumentOp(reg, "GET", "/admin/organization/ebook-requests", "listBloemAdminEbookRequests", "requests", "List ebook requests in the selected default organization.", bloemOrganizationContextSecurity, 200)
	admin.Description = queue + " Results are newest first."
	bloemRequestDocErrors(reg, &admin, bloemRequestAdminErrors(map[int]string{400: bloemRequestListQueryRule, 403: outsideDefaultAdmin}))
	registerBloemChiDocument[BloemBookRequestAdminListInput, BloemBookRequestListOutput](reg, admin)
	for _, action := range []struct{ name, id, summary, description, invalid, forbidden, conflict string }{
		{"approve", "approveBloemEbookRequest", "Approve a pending ebook request.", "", "", " requests_disabled: ebook requests are opted out.", "the request is no longer active, or it is not pending"},
		{"decline", "declineBloemEbookRequest", "Decline a pending ebook request and refund its quota slot.", "", "", "", "the request is no longer active, or it is not pending"},
		{"retry", "retryBloemEbookRequest", "Recover uncertain provider state or retry a safely failed ebook dispatch.", " Uncertain submissions are recovered through read-only provider checks; they are never blindly resubmitted.", " It is also returned when the retained library binding is no longer an eligible ebook library.", " requests_disabled: ebook requests or ebook acquisition are opted out.", "the request is not active and approved, its fulfilment is not failed or retrying, it has no dispatch, or its connection or library binding changed"}, //nolint:misspell // fulfilment is the persisted wire field.
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-requests/{id}/"+action.name, action.id, "requests", action.summary, bloemOrganizationContextSecurity, 200)
		op.Description = queue + " A matching expected_revision is required. Unknown JSON fields and extra bodies are rejected. Manual ebook completion is unavailable." + action.description
		bloemRequestDocErrors(reg, &op, bloemRequestAdminErrors(map[int]string{
			400: "invalid_request: " + bloemRequestActionBodyRule + "." + action.invalid,
			403: outsideDefaultAdmin + action.forbidden,
			404: bloemRequestNotFound("an ebook request"),
			409: "request_state_changed: expected_revision is stale, " + action.conflict + ".",
		}))
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}

	// Setup accepts either administrative scope. A platform context resolves
	// the deployment's default organization (see bloemEbookPlatformSetup).
	const authority = "Accepts an organization administrative context or a platform administrative context. A platform context acts on the deployment's default organization and requires the account's active membership there; an initializing default organization qualifies only while Silo switching is enabled. Changes and connection tests require platform administrator authority: an organization context must be for the default organization with platform_admin effective authority. Connection credentials are encrypted by the host and never included in responses. Responses are private and no-store."
	setupErrors := func(extra map[int]string) map[int]string {
		return bloemRequestDocMerge(map[int]string{
			401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions.",
			403: "request_forbidden: the account lacks current platform administrator authority or an active default-organization membership, or a change comes from an organization context that is not for the default organization or lacks platform_admin effective authority; insufficient_platform_authority: a platform context whose platform authority lapsed; organization_suspended: the organization is suspended.",
			500: "internal_error: the request store, platform authority check or plugin host failed.",
			503: "tenant_unavailable: administrative authority could not be checked; unavailable: ebook request setup is not configured.",
		}, extra)
	}
	setup := bloemChiDocumentOp(reg, "GET", "/admin/organization/ebook-request-integrations", "getBloemEbookRequestSetup", "requests", "Read ebook policy, connections and eligible libraries/installations.", bloemOrganizationContextSecurity, 200)
	setup.Security = append(setup.Security, map[string][]string{bloemPlatformContextSecurity: {}})
	setup.Description = authority + " Any administrator of the default organization may read the setup. An organization context for a hosted organization receives available=false and empty lists."
	bloemRequestDocErrors(reg, &setup, setupErrors(nil))
	registerBloemChiDocument[struct{}, BloemEbookRequestSetupOutput](reg, setup)

	connection := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-request-integrations", "createBloemEbookRequestIntegration", "requests", "Create an explicit Bookwarehouse ebook fulfillment connection.", bloemOrganizationContextSecurity, 201)
	connection.Security = append(connection.Security, map[string][]string{bloemPlatformContextSecurity: {}})
	connection.Description = authority + " Supported media types are fixed to book and EPUB is the default. HTTP requires explicit allow_http for a trusted local endpoint. Native storage installations cannot serve this capability."
	bloemRequestDocErrors(reg, &connection, setupErrors(map[int]string{400: "invalid_request: " + bloemRequestBodyRule + "; name (1 to 500 bytes), base_url (up to 4096 bytes) or api_key (up to 8192 bytes) missing or too long, or installation_id and capability_id not naming an eligible Bookwarehouse installation."}))
	registerBloemChiDocument[BloemEbookRequestConnectionInput, BloemEbookRequestIntegrationOutput](reg, connection)

	binding := bloemChiDocumentOp(reg, "PUT", "/admin/organization/ebook-request-integrations/{id}/library", "setBloemEbookRequestIntegrationLibrary", "requests", "Bind a connection to an eligible native Bookwarehouse ebook library.", bloemOrganizationContextSecurity, 200)
	binding.Security = append(binding.Security, map[string][]string{bloemPlatformContextSecurity: {}})
	binding.Description = authority + " library_id=0 clears the binding. Existing dispatched work retains its original library and configuration identity."
	bloemRequestDocErrors(reg, &binding, setupErrors(map[int]string{400: "invalid_request: " + bloemRequestBodyRule + "; a negative library_id, or an ID that is not an enabled, eligible connection or a library_id that is not an eligible native Bookwarehouse ebook library for this organization."}))
	registerBloemChiDocument[BloemEbookRequestLibraryInput, BloemEbookRequestIntegrationOutput](reg, binding)

	test := bloemChiDocumentOp(reg, "POST", "/admin/organization/ebook-request-integrations/{id}/test", "testBloemEbookRequestIntegration", "requests", "Run the Bookwarehouse plugin's connection test for a saved connection.", bloemOrganizationContextSecurity, 200)
	test.Security = append(test.Security, map[string][]string{bloemPlatformContextSecurity: {}})
	test.Description = authority + " The request has no body. A failed test is HTTP 200 with ok=false. A plugin message that repeats the saved API key is withheld."
	bloemRequestDocErrors(reg, &test, setupErrors(map[int]string{
		404: "not_found: the connection is absent or not usable by this organization's ebook lane.",
		409: "request_state_changed: the saved connection's installation or capability no longer matches the eligible connection.",
	}))
	registerBloemChiDocument[BloemRequestIntegrationTestInput, BloemRequestIntegrationTestOutput](reg, test)

	policy := bloemChiDocumentOp(reg, "PUT", "/admin/organization/ebook-request-policy", "setBloemEbookRequestPolicy", "requests", "Explicitly enable or disable ebook request admission and dispatch.", bloemOrganizationContextSecurity, 200)
	policy.Security = append(policy.Security, map[string][]string{bloemPlatformContextSecurity: {}})
	policy.Description = authority + " The default is false. Disabling prevents new requests and new provider work; work already submitted may continue at Bookwarehouse. History and local cancellation remain available."
	bloemRequestDocErrors(reg, &policy, setupErrors(map[int]string{400: "invalid_request: " + bloemRequestBodyRule + ", or enabled missing or null."}))
	registerBloemChiDocument[BloemEbookRequestPolicyInput, BloemEbookRequestPolicyOutput](reg, policy)
}
