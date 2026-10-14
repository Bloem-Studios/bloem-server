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
	op := bloemChiDocumentOp(reg, "POST", "/preview/events", "recordBloemPreviewEvent", "preview", "Increment a daily aggregate preview counter.", "bearerAuth", 204)
	op.Description = "Requires an authenticated preview session; ordinary accounts and API keys are refused. Accepts exactly one JSON object with one allowlisted event field, capped at 1 KiB. Counters retain only UTC day, event name and count; no identity or arbitrary payload. Per-process session limit: one per ten seconds, burst three. Aggregate limit: five per second, burst twenty. Retries increment again."
	op.Extensions = map[string]any{"x-silo-retry-safety": string(RetrySafetyNonRetryable)}
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "Invalid event or extra fields.", 429: "rate_limited: preview event budget exhausted."})
	op.Responses["429"].Headers = map[string]*huma.Header{"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying."}}
	registerBloemChiDocument[BloemPreviewEventInput, struct{}](reg, op)
}
