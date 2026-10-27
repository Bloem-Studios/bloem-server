package apiv2

import (
	"github.com/danielgtaylor/huma/v2"
)

type BloemPreviewEventInput struct {
	Body struct {
		Event string `json:"event" enum:"cta_shown,cta_clicked,session_ended"`
	}
}

func registerBloemPreviewEventsDocument(reg *Registry) {
	registerBloemPreviewPublicDocument(reg)
	op := bloemChiDocumentOp(reg, "POST", "/preview/events", "recordBloemPreviewEvent", "preview", "Increment a daily aggregate preview counter.", "bearerAuth", 204)
	op.Description = "Requires an authenticated preview session (a guest or a personal preview account); ordinary account sessions and API keys are refused. Accepts exactly one JSON object with one allowlisted event field, capped at 1 KiB. Counters retain only UTC day, event name and count; no identity or arbitrary payload. Per-process session limit: one per ten seconds, burst three. Aggregate limit: five per second, burst twenty. Guests also spend the preview's guest traffic budgets. Retries increment again. Responses are not cacheable."
	op.Extensions = map[string]any{"x-silo-retry-safety": string(RetrySafetyNonRetryable)}
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "invalid_request: the body is not exactly one JSON object with an allowlisted event, has unknown fields or exceeds 1 KiB.",
		503: "preview_unavailable: preview counters are unavailable; service_unavailable: the credential or guest session could not be checked; unavailable: the preview host settings could not be read.",
	})
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		401: "unauthorized: the bearer credential is missing, invalid or revoked, or a guest session is no longer valid because the preview was disabled or the request used another host. Preview host denials add code preview_session_invalid or preview_host.",
		403: "forbidden with code preview_only: the caller is not a preview principal or has no session; forbidden: another credential gate refused the request.",
		429: "rate_limited: this session's event budget (Retry-After 10) or the aggregate event budget is exhausted, or, with code preview_busy, the guest's request budget; the preview's shared guest traffic budget also returns rate_limited.",
	})
	op.Responses["429"].Headers = map[string]*huma.Header{"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying."}}
	registerBloemChiDocument[BloemPreviewEventInput, struct{}](reg, op)
}
