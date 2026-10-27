package apiv2

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
)

type BloemPreviewSessionInput struct {
	Body struct {
		Ticket       string `json:"ticket" minLength:"1" writeOnly:"true"`
		RefreshToken string `json:"refresh_token,omitempty" writeOnly:"true" doc:"The current guest refresh token, if any. A valid token may reuse its bounded guest session."`
	}
}

type BloemPreviewSession struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type" enum:"Bearer"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
	Item         string    `json:"item" doc:"Initial catalog item verified from the signed launch ticket; empty when absent."`
	Ref          string    `json:"ref" doc:"Referral verified from the signed launch ticket; empty when absent."`
}

type BloemPreviewSessionOutput struct{ Body BloemPreviewSession }

type BloemPreviewPublicStatus struct {
	Enabled       bool   `json:"enabled"`
	Brand         string `json:"brand,omitempty"`
	StorefrontURL string `json:"storefront_url,omitempty"`
	LaunchURL     string `json:"launch_url,omitempty"`
	CTAURL        string `json:"cta_url,omitempty"`
	CTALabel      string `json:"cta_label,omitempty"`
}

type BloemPreviewPublicStatusOutput struct{ Body BloemPreviewPublicStatus }

// bloemPreviewDocRetryAfter adds Retry-After to the listed statuses.
func bloemPreviewDocRetryAfter(op *huma.Operation, statuses ...string) {
	for _, code := range statuses {
		op.Responses[code].Headers = map[string]*huma.Header{
			"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying."},
		}
	}
}

// The preview host guard runs ahead of routing for every request to
// /api/bloem/v1/preview/: it spends the shared guest traffic budget and
// refuses credentials presented on the wrong host.
const (
	bloemPreviewGuardLimit   = "rate_limited: the preview's shared guest traffic budget (sixty requests per second, burst 120) is exhausted; retry after one second."
	bloemPreviewGuardHostRun = "unauthorized: an account or API-key credential was sent to the preview host (code preview_host), or a guest credential to another host (code preview_session_invalid)."
)

func registerBloemPreviewPublicDocument(reg *Registry) {
	status := bloemOp("GET", "/preview/status", "getBloemPreviewPublicStatus", "preview", "Discover preview availability and host-specific presentation.")
	status.Security = []map[string][]string{}
	status.Description = "Public. Successful responses are not cacheable. Returns enabled on all served hosts. Only the configured preview host receives brand and storefront, launch and subscription links, even when disabled. Never exposes capacity, keys or account identity. No credential is needed; one that is sent is still checked against the preview host."
	status.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &status, map[int]string{401: bloemPreviewGuardHostRun})
	bloemDocumentErrors[BloemNativeError](reg, &status, map[int]string{
		429: bloemPreviewGuardLimit,
		503: "unavailable: preview status, or the preview host settings while a credential was sent, could not be read.",
	})
	bloemPreviewDocRetryAfter(&status, "429")
	registerBloemChiDocument[struct{}, BloemPreviewPublicStatusOutput](reg, status)

	exchange := bloemOp("POST", "/preview/sessions", "createBloemPreviewGuestSession", "preview", "Exchange a signed launch ticket for a bounded guest session.")
	exchange.DefaultStatus = 201
	exchange.Security = []map[string][]string{}
	exchange.Description = "Public ticket exchange on the configured preview host only. Accepts a JSON body up to 16 KiB. The Ed25519 ticket must match the issuer, server identity audience and host and be unexpired and unused. Consumes its ticket ID atomically. An existing valid guest refresh token may reuse a session without extending its absolute lifetime. Responses are not cacheable. The per-client exchange budget is twenty per hour; guest capacity and per-client eviction remain server-controlled. No credential is needed; one that is sent is still checked against the preview host."
	exchange.Extensions = map[string]any{"x-silo-retry-safety": string(RetrySafetyNonRetryable)}
	exchange.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &exchange, map[int]string{401: bloemPreviewGuardHostRun})
	bloemDocumentErrors[BloemNativeError](reg, &exchange, map[int]string{
		400: "invalid_request: a launch ticket is required in a valid bounded JSON body.",
		403: "preview_ticket_invalid: the ticket is invalid, expired or already consumed.",
		404: "preview_unavailable: preview is disabled or the request used another host.",
		429: "rate_limited: too many starts for this client (twenty per hour), or the preview's shared guest traffic budget is exhausted. Retry-After gives the wait.",
		500: "internal_error: the preview session could not start.",
		503: "preview_busy: guest capacity is exhausted; unavailable: the preview host settings could not be read while a credential was sent.",
	})
	bloemPreviewDocRetryAfter(&exchange, "429", "503")
	registerBloemChiDocument[BloemPreviewSessionInput, BloemPreviewSessionOutput](reg, exchange)
}
