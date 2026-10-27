package apiv2

import (
	"reflect"
	"regexp"
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

func bloemAdminDocumentOp(reg *Registry, method, path, id, summary, security string, status int) huma.Operation {
	op := bloemChiDocumentOp(reg, method, path, id, "administration", summary, security, status)
	bloemDocumentPathParameters(&op)
	if strings.Contains(path, "{") {
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found: resource is invalid, absent or hidden from this administrative scope."})
	}
	return op
}

func bloemAdminDocumentMutation(reg *Registry, op *huma.Operation) {
	bloemDocumentValidation(reg, op)
	bloemDocumentErrors[BloemNativeRevisionConflict](reg, op, map[int]string{409: "The reviewed authorization revision or confirmation is stale, or the mutation conflicts with current state. Reload before retrying. current_revision is present only for authorization_state_changed."})
	op.Description += " JSON decoding rejects unknown fields, trailing JSON and bodies exceeding 32 KiB. expected_revision fields are JSON preconditions; these handlers do not use If-Match."
}

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

func bloemDocumentPlatformAPIKey(op *huma.Operation, scheme string) {
	op.Security = append(op.Security, map[string][]string{scheme: {}})
	op.Description += " Also accepts a bearer API key owned by a currently enabled platform administrator. Unscoped keys are supported; scoped keys need the permission named by this security scheme. An organization context cannot broaden its authority."
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
