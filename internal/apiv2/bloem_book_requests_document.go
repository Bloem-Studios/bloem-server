package apiv2

import (
	"maps"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/bookrequests"
)

type BloemBookRequestViewerInput struct {
	ProfileID    string `header:"X-Profile-Id" doc:"The acting profile, which must be verified in the authenticated account's current organization. Required except for a direct-profile session, which acts as its bound profile and is refused if it names another."`
	ProfileToken string `header:"X-Profile-Token" doc:"PIN verification when required."`
}
type BloemBookRequestListInput struct {
	BloemBookRequestViewerInput
	Limit  int `query:"limit" default:"100" maximum:"100" minimum:"1"`
	Offset int `query:"offset" default:"0" minimum:"0"`
}
type BloemBookRequestCreateInput struct {
	BloemBookRequestViewerInput
	IdempotencyKey string `header:"Idempotency-Key" required:"true" minLength:"16" maxLength:"128" doc:"A durable key scoped to organization, account and profile, without spaces, tabs or line breaks. Reuse only for the same normalized request."`
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

// Shared error text for the request lanes (audiobook, ebook and audiobook
// acquisition). Each operation lists exactly the statuses its middleware,
// handler and service can write; the chi defaults are replaced, not extended.
const (
	bloemRequestBodyRule        = "invalid or oversized JSON (16 KiB), unknown fields or more than one JSON value"
	bloemRequestActionBodyRule  = bloemRequestBodyRule + ", expected_revision missing or not positive, or reason over 2000 bytes"
	bloemRequestListQueryRule   = "invalid_request: limit must be 1 to 100 and offset nonnegative."
	bloemRequestViewer403       = "viewer_required: no verified profile in an active organization membership, or the profile belongs to another organization; profile_unverified: PIN proof is missing or invalid. The credential and tenant gates may also return forbidden (with code preview_only for a preview principal), password_change_required or organization_suspended."
	bloemRequestAdmin403        = "insufficient_organization_authority: the context is not organization-scoped (a platform context whose platform authority lapsed receives insufficient_platform_authority); organization_suspended: the organization is suspended."
	bloemRequestOrgAdminMessage = "Requires an organization administrative-context token; platform-scoped contexts are refused."
)

// bloemRequestNotFound describes a request lookup that found nothing: each
// lane reads only its own media type, organization and, for viewers, profile.
func bloemRequestNotFound(owner string) string {
	return "not_found: the request is absent, its ID is not a UUID, or it is not " + owner + " in this organization."
}

// bloemRequestDocErrors replaces op's error responses with errs.
func bloemRequestDocErrors(reg *Registry, op *huma.Operation, errs map[int]string) {
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, op, errs)
}

// bloemRequestDocMerge appends extra text to the base description of each status.
func bloemRequestDocMerge(base, extra map[int]string) map[int]string {
	out := maps.Clone(base)
	for status, text := range extra {
		if prior, ok := out[status]; ok {
			out[status] = prior + " " + text
		} else {
			out[status] = text
		}
	}
	return out
}

// bloemRequestViewerErrors covers the shared viewer chain on /book-requests and
// /ebook-requests: authentication, tenant projection, the optional API rate
// limit, viewer access, the profile header and the handler's viewer check.
func bloemRequestViewerErrors(extra map[int]string) map[int]string {
	return bloemRequestDocMerge(map[int]string{
		400: "bad_request: X-Profile-Id is required for this session.",
		401: "unauthorized, tenant_session_required or authorization_state_stale: the bearer credential is missing, invalid, revoked or no longer current. On the preview host, account and API-key credentials are refused with code preview_host.",
		403: bloemRequestViewer403,
		404: "not_found: the selected profile does not exist for this account.",
		500: "internal_error: viewer access or the request store failed.",
		503: "service_unavailable, tenant_unavailable or unavailable: the credential, tenant, viewer policy or request service could not be checked; retry later.",
	}, extra)
}

// bloemRequestAdminErrors covers the administrative-context middleware and the
// organization-context check of the request queue and setup handlers.
func bloemRequestAdminErrors(extra map[int]string) map[int]string {
	return bloemRequestDocMerge(map[int]string{
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer matches its login session, membership, role or revisions.",
		403: bloemRequestAdmin403,
		500: "internal_error: the request store failed.",
		503: "tenant_unavailable: administrative authority could not be checked; unavailable: the request service is not configured.",
	}, extra)
}

// bloemRequestDocViewer documents the viewer chain's statuses with extra
// operation text. 401 and 403 use the coded envelope: preview principals are
// refused with error=forbidden and code=preview_only, and the preview host
// refuses account credentials with code=preview_host.
func bloemRequestDocViewer(reg *Registry, op *huma.Operation, extra map[int]string, quota string) {
	errs := bloemRequestViewerErrors(extra)
	coded := map[int]string{401: errs[401], 403: errs[403]}
	delete(errs, 401)
	delete(errs, 403)
	bloemRequestDocErrors(reg, op, errs)
	bloemDocumentErrors[BloemNativeError](reg, op, coded)
	bloemRequestDocViewerRateLimit(reg, op, quota)
}

// bloemRequestDocViewerRateLimit documents the API rate limit that guards the
// viewer routes when it is enabled. quota adds the lane's own quota refusal,
// which uses the plain native error envelope on the same status.
func bloemRequestDocViewerRateLimit(reg *Registry, op *huma.Operation, quota string) {
	bloemDocumentRateLimit(reg, op)
	limited := op.Responses["429"]
	limited.Description = "rate_limit_exceeded: the configured API rate limit was exceeded; the Retry-After and X-RateLimit headers apply to this code only."
	if quota == "" {
		return
	}
	limited.Description = quota + " " + limited.Description
	schemas := reg.api.OpenAPI().Components.Schemas
	limited.Content["application/json"].Schema = &huma.Schema{AnyOf: []*huma.Schema{
		schemas.Schema(reflect.TypeFor[BloemNativeError](), true, ""),
		schemas.Schema(reflect.TypeFor[BloemNativeRateLimitError](), true, ""),
	}}
}

// bloemRequestDocReplay documents the idempotent replay of a filing: the same
// body as a new filing, under HTTP 200.
func bloemRequestDocReplay(reg *Registry, path string) {
	registered := reg.api.OpenAPI().Paths[path].Post
	created := *registered.Responses["201"]
	created.Description = "A new request was filed."
	registered.Responses["201"] = &created
	replay := created
	replay.Description = "The Idempotency-Key was already used for the same normalized input; the existing request is returned unchanged."
	registered.Responses["200"] = &replay
}

func registerBloemBookRequestsDocument(reg *Registry) {
	registerBloemEbookRequestsDocument(reg)
	registerBloemAudiobookAcquisitionDocument(reg)
	//nolint:misspell // awaiting_fulfilment is the established wire enum.
	const description = "Bloem-owned audiobook requests use a separate table and independent quota counters. Approval and quota values inherit current profile-group, membership/account and global request policy. A verified profile and current organization membership are required; hosted organizations may file requests. Ordinary callers can see or cancel only their own profile's requests in this organization. Approved requests stay approved/awaiting_fulfilment while no acquisition backend is configured. Library completion requires every supplied ISBN/ASIN to match one audiobook with a media-file row in an organization-accessible audiobook library; title-only matches and ebooks never fulfill an audiobook request. Catalog presence does not preflight filesystem or storage-provider liveness. Ebook request routes are not enabled by this capability. Responses are private and no-store."

	capability := bloemChiDocumentOp(reg, "GET", "/book-requests/capability", "getBloemBookRequestCapability", "requests", "Read audiobook request capability and current viewer policy.", "bearerAuth", 200)
	capability.Description = description
	bloemRequestDocViewer(reg, &capability, map[int]string{403: "request_forbidden: the profile, account, membership or organization no longer qualifies for request policy."}, "")
	registerBloemChiDocument[BloemBookRequestViewerInput, BloemBookRequestCapabilityOutput](reg, capability)

	list := bloemChiDocumentOp(reg, "GET", "/book-requests/mine", "listBloemBookRequests", "requests", "List this profile's audiobook requests in the current organization.", "bearerAuth", 200)
	list.Description = description + " Results are newest first."
	bloemRequestDocViewer(reg, &list, map[int]string{400: bloemRequestListQueryRule}, "")
	registerBloemChiDocument[BloemBookRequestListInput, BloemBookRequestListOutput](reg, list)

	create := bloemChiDocumentOp(reg, "POST", "/book-requests", "createBloemBookRequest", "requests", "File an audiobook request.", "bearerAuth", 201)
	create.Description = description + " ISBN-10 is normalized to ISBN-13; ASIN is uppercased. A title plus author can be filed without identifiers and can be fulfilled manually. An identical Idempotency-Key replay returns HTTP 200 with the existing request, including after completion or withdrawal; changed normalized input returns 409. A different key for an active identity returns 409 without revealing another profile's request. Unknown JSON fields and extra bodies are rejected."
	bloemRequestDocViewer(reg, &create, map[int]string{
		400: "invalid_request: " + bloemRequestBodyRule + "; a missing title, a title or author over 500 bytes, a note over 2000 bytes, an invalid ISBN or ASIN, no author when neither identifier is given, or an Idempotency-Key that is not 16 to 128 characters without whitespace.",
		403: "request_forbidden: request policy (profile group, account limits or approval mode) does not let this profile file requests; requests_disabled: requests are disabled.",
		409: "already_requested: an active request for the same ISBN, ASIN or title and author exists in this organization; request_state_changed: the Idempotency-Key was used with different normalized input.",
	}, "request_quota_exceeded: the independent audiobook quota is exhausted (plain native error);")
	registerBloemChiDocument[BloemBookRequestCreateInput, BloemBookRequestOutput](reg, create)
	bloemRequestDocReplay(reg, create.Path)
	bloemRequestDocCreateBody(reg)

	get := bloemChiDocumentOp(reg, "GET", "/book-requests/{id}", "getBloemBookRequest", "requests", "Read this profile's audiobook request.", "bearerAuth", 200)
	get.Description = description
	bloemRequestDocViewer(reg, &get, map[int]string{404: bloemRequestNotFound("this profile's audiobook request")}, "")
	registerBloemChiDocument[BloemBookRequestGetInput, BloemBookRequestOutput](reg, get)

	cancel := bloemChiDocumentOp(reg, "POST", "/book-requests/{id}/cancel", "cancelBloemBookRequest", "requests", "Withdraw a pending or approved audiobook request.", "bearerAuth", 200)
	cancel.Description = description + " expected_revision must match the current row. Withdrawal retains its quota usage."
	bloemRequestDocViewer(reg, &cancel, map[int]string{
		400: "invalid_request: " + bloemRequestActionBodyRule + ".",
		404: bloemRequestNotFound("this profile's audiobook request"),
		409: "request_state_changed: expected_revision is stale, or the request is completed or no longer active.",
	}, "")
	registerBloemChiDocument[BloemBookRequestCancelInput, BloemBookRequestOutput](reg, cancel)

	adminList := bloemChiDocumentOp(reg, "GET", "/admin/organization/book-requests", "listBloemAdminBookRequests", "requests", "List audiobook requests in the selected administrative organization.", bloemOrganizationContextSecurity, 200)
	adminList.Description = bloemRequestOrgAdminMessage + " Results are newest first; pagination is applied after organization isolation. Responses are private and no-store."
	bloemRequestDocErrors(reg, &adminList, bloemRequestAdminErrors(map[int]string{400: bloemRequestListQueryRule}))
	registerBloemChiDocument[BloemBookRequestAdminListInput, BloemBookRequestListOutput](reg, adminList)
	for _, action := range []struct{ name, id, summary, description, conflict string }{
		{"approve", "approveBloemBookRequest", "Approve an audiobook request; keep it awaiting fulfillment.", "", "the request is no longer active, or it is not pending"},
		{"decline", "declineBloemBookRequest", "Decline a pending audiobook request and refund its quota slot.", "", "the request is no longer active, or it is not pending"},
		{"fulfil", "fulfilBloemBookRequest", "Manually mark an audiobook request fulfilled.", " Any active, not yet completed request can be fulfilled. Manual fulfilment records fulfilment=manual without claiming a playable library item.", "or the request is completed or no longer active"}, //nolint:misspell // Preserve the established route, operation identifiers and wire value.
	} {
		op := bloemChiDocumentOp(reg, "POST", "/admin/organization/book-requests/{id}/"+action.name, action.id, "requests", action.summary, bloemOrganizationContextSecurity, 200)
		op.Description = bloemRequestOrgAdminMessage + " A matching expected_revision is required. Other organizations' requests return 404. Unknown JSON fields and extra bodies are rejected. Responses are private and no-store." + action.description
		bloemRequestDocErrors(reg, &op, bloemRequestAdminErrors(map[int]string{
			400: "invalid_request: " + bloemRequestActionBodyRule + ".",
			404: bloemRequestNotFound("an audiobook request"),
			409: "request_state_changed: expected_revision is stale, " + action.conflict + ".",
		}))
		registerBloemChiDocument[BloemBookRequestActionInput, BloemBookRequestOutput](reg, op)
	}
}

// bloemRequestDocCreateBody corrects the filing body's required set: only the
// title is always required, because an ISBN or ASIN replaces the author.
func bloemRequestDocCreateBody(reg *Registry) {
	schema := reg.api.OpenAPI().Components.Schemas.SchemaFromRef(reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[bookrequests.CreateInput](), true, "").Ref)
	schema.Required = []string{"title"}
	schema.Properties["title"].Description = "Trimmed; 1 to 500 bytes."
	schema.Properties["author"].Description = "At most 500 bytes. Required when neither isbn nor asin is supplied."
	schema.Properties["isbn"].Description = "Valid ISBN-10 or ISBN-13; normalized to ISBN-13."
	schema.Properties["asin"].Description = "Ten letters or digits; uppercased."
	schema.Properties["note"].Description = "At most 2000 bytes."
}
