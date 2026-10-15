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

func registerBloemPreviewPublicDocument(reg *Registry) {
	status := bloemOp("GET", "/preview/status", "getBloemPreviewPublicStatus", "preview", "Discover preview availability and host-specific presentation.")
	status.Security = []map[string][]string{}
	status.Description = "Public and not cacheable. Returns enabled on all served hosts. Only the configured preview host receives brand and storefront, launch and subscription links, even when disabled. Never exposes capacity, keys or account identity."
	status.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &status, map[int]string{503: "unavailable: preview status could not be read."})
	registerBloemChiDocument[struct{}, BloemPreviewPublicStatusOutput](reg, status)

	exchange := bloemOp("POST", "/preview/sessions", "createBloemPreviewGuestSession", "preview", "Exchange a signed launch ticket for a bounded guest session.")
	exchange.DefaultStatus = 201
	exchange.Security = []map[string][]string{}
	exchange.Description = "Public ticket exchange on the configured preview host only. Accepts a JSON body up to 16 KiB. The Ed25519 ticket must match the issuer, server identity audience and host and be unexpired and unused. Consumes its ticket ID atomically. An existing valid guest refresh token may reuse a session without extending its absolute lifetime. Responses are not cacheable. The per-client exchange budget is twenty per hour; guest capacity and per-client eviction remain server-controlled."
	exchange.Extensions = map[string]any{"x-silo-retry-safety": string(RetrySafetyNonRetryable)}
	exchange.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &exchange, map[int]string{
		400: "invalid_request: a launch ticket is required in a valid bounded JSON body.",
		403: "preview_ticket_invalid: the ticket is invalid, expired or already consumed.",
		404: "preview_unavailable: preview is disabled or the request used another host.",
		429: "rate_limited: too many starts for this client.",
		500: "internal_error: the preview session could not start.",
		503: "preview_busy: guest capacity is exhausted.",
	})
	for _, code := range []string{"429", "503"} {
		exchange.Responses[code].Headers = map[string]*huma.Header{
			"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds before retrying."},
		}
	}
	registerBloemChiDocument[BloemPreviewSessionInput, BloemPreviewSessionOutput](reg, exchange)
}
