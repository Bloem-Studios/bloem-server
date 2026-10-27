package apiv2

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// These wrappers use Huma only to describe existing chi JSON bodies. Path and
// query parameters below are explicit because the same wire body has aliases.
type bloemJSONInput[T any] struct{ Body T }
type bloemOptionalJSONInput[T any] struct {
	Body *T `required:"false"`
}
type bloemJSONOutput[T any] struct{ Body T }

var bloemDocumentPathParameter = regexp.MustCompile(`\{([^}]+)\}`)

func bloemDocumentPathParameters(op *huma.Operation) {
	for _, match := range bloemDocumentPathParameter.FindAllStringSubmatch(op.Path, -1) {
		name := match[1]
		schema := &huma.Schema{Type: "string"}
		switch name {
		case "account_id", "user_id", "folder_id":
			schema.Type, schema.Format = "integer", "int64"
		case "organization_id", "membership_id", "cohort_id":
			schema.Format = "uuid"
		case "id":
			if strings.Contains(op.Path, "/platform/organizations/") {
				schema.Format = "uuid"
			} else {
				schema.Type, schema.Format = "integer", "int64"
			}
		}
		op.Parameters = append(op.Parameters, &huma.Param{Name: name, In: "path", Required: true, Schema: schema})
	}
}

func bloemDocumentQuery(op *huma.Operation, name, description, kind string) {
	op.Parameters = append(op.Parameters, &huma.Param{Name: name, In: "query", Description: description, Schema: &huma.Schema{Type: kind}})
}

const (
	bloemAdminContextUnauthorized       = "tenant_session_required: the bearer is not a valid administrative-context token; authorization_state_stale: its login session, account, membership or revisions changed. Exchange a new context through /admin/session."
	bloemPlatformContextForbidden       = "insufficient_platform_authority: the context is not platform-scoped or its account is no longer a platform administrator; organization_suspended: an organization context names a suspended organization or membership."
	bloemOrganizationContextForbidden   = "insufficient_organization_authority: the context is not organization-scoped; organization_suspended: the organization or membership is suspended; insufficient_platform_authority: a platform context's account is no longer a platform administrator."
	bloemOrganizationContextUnavailable = "tenant_unavailable: administrative authorization or the administration store is unavailable."
)

func bloemAdminDocumentOp(reg *Registry, method, path, id, summary, security string, status int) huma.Operation {
	op := bloemChiDocumentOp(reg, method, path, id, "administration", summary, security, status)
	bloemDocumentPathParameters(&op)
	if strings.Contains(path, "{") {
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found: resource is invalid, absent or hidden from this administrative scope."})
	}
	// Name the codes written by AdminContextMiddleware and the handlers' scope
	// and availability checks. Platform entitlement handlers report their own
	// store as entitlements_unavailable.
	unavailable := bloemOrganizationContextUnavailable
	if strings.HasPrefix(path, "/admin/platform/") && strings.Contains(path, "/entitlement") {
		unavailable = "tenant_unavailable: administrative authorization is unavailable; entitlements_unavailable: entitlement administration is unavailable."
	}
	switch security {
	case "bloemPlatformContext":
		bloemDocumentDescribe(&op, map[int]string{
			401: bloemAdminContextUnauthorized,
			403: bloemPlatformContextForbidden,
			503: unavailable,
		})
	case "bloemOrganizationContext":
		bloemDocumentDescribe(&op, map[int]string{
			401: bloemAdminContextUnauthorized,
			403: bloemOrganizationContextForbidden,
			503: unavailable,
		})
	}
	return op
}

// bloemDocumentDescribe replaces the descriptions of documented statuses and
// keeps their schemas, so an operation can name the codes it actually writes.
func bloemDocumentDescribe(op *huma.Operation, descriptions map[int]string) {
	for status, description := range descriptions {
		response := op.Responses[strconv.Itoa(status)]
		if response == nil {
			panic("bloem document: " + op.OperationID + " does not document " + strconv.Itoa(status))
		}
		response.Description = description
	}
}

// bloemDocumentOmit removes default statuses that neither the operation's
// handler nor its middleware can produce.
func bloemDocumentOmit(op *huma.Operation, statuses ...int) {
	for _, status := range statuses {
		key := strconv.Itoa(status)
		if op.Responses[key] == nil {
			panic("bloem document: " + op.OperationID + " does not document " + key)
		}
		delete(op.Responses, key)
	}
}

func bloemAdminDocumentMutation(reg *Registry, op *huma.Operation) {
	bloemDocumentValidation(reg, op)
	bloemDocumentErrors[BloemNativeRevisionConflict](reg, op, map[int]string{409: "The reviewed authorization revision or confirmation is stale, or the mutation conflicts with current state. Reload before retrying. current_revision is present only for authorization_state_changed."})
	op.Description += " JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match."
}

// Shared conflict and validation wording for handlers that compare a JSON
// expected_revision with the organization's policy revision, and for people
// policy jobs in both administrative scopes.
const (
	bloemOrganizationPolicyConflict = "authorization_state_changed: expected_revision is not the organization's policy_revision; current_revision carries the current value."
	bloemPolicyJobConflict          = "idempotency_conflict: the idempotency_key was already used for a different command; policy_confirmation_stale: the preview changed or expired; selection_expired: the selection expired; authorization_state_changed: authorization changed since the preview."
	bloemPolicyJobValidation        = "validation_failed: idempotency_key is missing or longer than 128 characters, selection_token or confirmation_token is missing, or the command is invalid."
)

const (
	bloemEntitlementApplyKeyRule = "Repeating an apply with the same idempotency_key returns the original result without applying again."
	bloemPolicyJobKeyRule        = "An idempotency_key of 1 to 128 characters is required, in the body or the Idempotency-Key header. Repeating the command with the same key returns the original job; the same key with a different command returns 409 idempotency_conflict."
)

// bloemDocumentCommandKey documents a command whose own idempotency key makes
// a retry return the original result. Idempotency-Key is that key sent as a
// header. Bloem's general lifecycle receipts, which took the header on every
// administrative mutation, were removed in b017bfe84; the other mutations
// ignore it.
func bloemDocumentCommandKey(op *huma.Operation, rule string) {
	op.Parameters = append(op.Parameters, &huma.Param{Name: "Idempotency-Key", In: "header", Description: "The command's idempotency_key, sent as a header. When the body also carries idempotency_key, the two must match.", Schema: &huma.Schema{Type: "string"}})
	op.Description += " " + rule + " A header key that differs from the body's returns 409 idempotency_key_conflict."
	if conflict := op.Responses["409"]; conflict != nil {
		conflict.Description += " idempotency_key_conflict: the Idempotency-Key header differs from the body's idempotency_key."
	}
}

// The sa_ alternative runs RequireAuth instead of AdminContextMiddleware, then
// the handler's platform-administrator check; add the codes that path writes.
func bloemDocumentPlatformAPIKey(op *huma.Operation, scheme string) {
	op.Security = append(op.Security, map[string][]string{scheme: {}})
	op.Description += " Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority."
	for status, addition := range map[int]string{
		401: " With an API key, unauthorized: the key is invalid or its account is disabled.",
		403: " With an API key, forbidden: the key's scopes do not permit this route; insufficient_platform_authority: its account is not an enabled platform administrator.",
		503: " With an API key, service_unavailable: the key could not be checked; retry after Retry-After.",
	} {
		op.Responses[strconv.Itoa(status)].Description += addition
	}
}

// Template availability uses the plain native error, while invalid policy fields
// use the field-addressable validation envelope on the same status.
func bloemDocumentTemplateValidation(reg *Registry, op *huma.Operation) {
	if !strings.Contains(op.Path, "/entitlement") {
		return
	}
	schemas := reg.api.OpenAPI().Components.Schemas
	op.Responses["422"] = &huma.Response{Description: "validation_failed with fields, or template_unavailable without fields.", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{AnyOf: []*huma.Schema{schemas.Schema(reflect.TypeFor[BloemNativeValidationError](), true, ""), schemas.Schema(reflect.TypeFor[BloemNativeError](), true, "")}}}}}
}
