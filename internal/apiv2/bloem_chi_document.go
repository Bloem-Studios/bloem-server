package apiv2

import (
	"context"
	"net/http"
	"reflect"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
)

// These declarations describe existing chi handlers only. GenerateBloemOpenAPI
// supplies the noop adapter; none of these registrations mounts a runtime route.
// Use Huma's typed schema generation without Register's v2 gate declarations:
// those would advertise acting-admin authority, Problem responses, If-Match
// conventions and statuses that these native handlers do not implement. Chi's
// collection paths also retain their mounted trailing slash.
func registerBloemChiDocument[I, O any](reg *Registry, op huma.Operation) {
	if _, ok := reg.api.Adapter().(noopAdapter); !ok {
		panic("Bloem chi documentation requires the OpenAPI noop adapter")
	}
	if name := multipartFormName(reflect.TypeFor[I]()); name != "" {
		op.Metadata = map[string]any{metaFormSchema: name}
	}
	huma.Register(reg.api, op, func(context.Context, *I) (*O, error) {
		return new(O), nil
	})
}

// BloemNativeError is the JSON envelope written by the native chi handlers and
// their authority middleware. It is distinct from the v2 Problem document.
type BloemNativeError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type BloemNativeValidationError struct {
	Error   string            `json:"error" enum:"validation_failed"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields" doc:"Validation messages keyed by request field."`
}

type BloemNativeRevisionConflict struct {
	Error           string `json:"error"`
	Message         string `json:"message"`
	CurrentRevision int64  `json:"current_revision,omitempty" doc:"Current organization policy revision when error is authorization_state_changed. Absent for invitation_not_claimable."`
}

type BloemNativeRateLimitError struct {
	Error      string `json:"error" enum:"rate_limit_exceeded"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after" doc:"Seconds to wait before retrying."`
}

func bloemDocumentRateLimit(reg *Registry, op *huma.Operation) {
	bloemDocumentErrors[BloemNativeRateLimitError](reg, op, map[int]string{429: "The configured rate limit was exceeded."})
	op.Responses["429"].Headers = map[string]*huma.Header{
		"Retry-After":           {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds to wait before retrying."},
		"X-RateLimit-Limit":     {Schema: &huma.Schema{Type: "integer"}},
		"X-RateLimit-Remaining": {Schema: &huma.Schema{Type: "integer"}},
		"X-RateLimit-Reset":     {Schema: &huma.Schema{Type: "integer"}, Description: "Reset time as Unix seconds."},
	}
}

func bloemChiDocumentOp(reg *Registry, method, path, id, tag, summary, security string, status int) huma.Operation {
	op := bloemOp(method, path, id, tag, summary)
	op.DefaultStatus = status
	op.Security = []map[string][]string{{security: {}}}
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		401: "Authentication or a current session of the required token type is missing; administrative routes require an administrative-context token.",
		403: "The authenticated caller lacks the required authority or profile verification.",
		503: "The feature or its authority dependencies are unavailable.",
	})
	return op
}

func bloemDocumentErrors[T any](reg *Registry, op *huma.Operation, descriptions map[int]string) {
	schema := reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[T](), true, "")
	for status, description := range descriptions {
		op.Responses[strconv.Itoa(status)] = &huma.Response{
			Description: description,
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schema},
			},
		}
	}
}

func bloemDocumentValidation(reg *Registry, op *huma.Operation) {
	bloemDocumentErrors[BloemNativeError](reg, op, map[int]string{
		http.StatusBadRequest: "invalid_request: invalid JSON, unknown fields, trailing JSON or an administrative body exceeding 32 KiB.",
	})
	bloemDocumentErrors[BloemNativeValidationError](reg, op, map[int]string{
		http.StatusUnprocessableEntity: "validation_failed: see fields for the invalid values.",
	})
}
