package apiv2

import (
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/preview"
)

type BloemPreviewIssuerInput struct {
	Body struct {
		Issuer string `json:"issuer" minLength:"1" maxLength:"128"`
	}
}
type BloemPreviewAccountOutput struct {
	Body struct {
		AccountID int `json:"account_id"`
	}
}
type BloemPreviewSettingsInput struct{ Body preview.SettingsPatch }
type BloemPreviewSettingsOutput struct {
	Body struct {
		Enabled           bool     `json:"enabled"`
		Host              string   `json:"host"`
		Issuer            string   `json:"issuer"`
		AccountID         int      `json:"account_id" readOnly:"true"`
		TicketPublicKeys  []string `json:"ticket_public_keys" readOnly:"true" nullable:"false"`
		SessionTTLSeconds int      `json:"session_ttl_seconds"`
		IdleTTLSeconds    int      `json:"idle_ttl_seconds"`
		MaxConcurrent     int      `json:"max_concurrent"`
		Brand             string   `json:"brand"`
		CTAURL            string   `json:"cta_url"`
		CTALabel          string   `json:"cta_label"`
		ItemCTALabel      string   `json:"item_cta_label"`
		CTAMessage        string   `json:"cta_message"`
		SubscribeLabel    string   `json:"subscribe_label"`
		SubscribeURL      string   `json:"subscribe_url"`
		StorefrontURL     string   `json:"storefront_url"`
		LaunchURL         string   `json:"launch_url"`
		MaturityCeiling   string   `json:"maturity_ceiling"`
	}
}
type BloemPreviewLibrariesInput struct {
	Body struct {
		Issuer     string `json:"issuer" minLength:"1" maxLength:"128"`
		LibraryIDs []int  `json:"library_ids" nullable:"false" maxItems:"1000" doc:"An empty array grants no libraries. All IDs must exist and be enabled."`
	}
}
type BloemPreviewLibrariesOutput struct {
	Body struct {
		TemplateRevision int64 `json:"template_revision"`
	}
}
type BloemPreviewKeysInput struct {
	Body struct {
		Issuer     string   `json:"issuer" minLength:"1" maxLength:"128"`
		PublicKeys []string `json:"public_keys" nullable:"false" minItems:"1" maxItems:"2" uniqueItems:"true" doc:"Distinct base64-encoded 32-byte Ed25519 public keys. Replaces the current allowlist; two keys permit overlap during rotation."`
	}
}
type BloemPreviewVerifyInput struct {
	Body struct {
		Ticket string `json:"ticket" minLength:"1" writeOnly:"true"`
	}
}
type BloemPreviewVerifyOutput struct {
	Body struct {
		Valid        bool      `json:"valid"`
		AccountReady bool      `json:"account_ready"`
		Item         string    `json:"item"`
		ExpiresAt    time.Time `json:"expires_at"`
	}
}
type BloemPreviewCapacityOutput struct{ Body preview.Capacity }
type BloemPreviewCatalogInput struct {
	Query      string `query:"query" maxLength:"256"`
	Provider   string `query:"provider" enum:"tmdb,imdb,tvdb"`
	ProviderID string `query:"provider_id" maxLength:"128"`
}
type BloemPreviewCatalogOutput struct {
	Body struct {
		Items []preview.CatalogItem `json:"items" nullable:"false" maxItems:"50"`
	}
}

// previewAdminDocumentOp documents one Platform preview administration route.
// Both credential formats reach the handler through their own validators
// (bloemEitherPlatformCredential); errs adds the operation's own 400, 403 and
// 409 text to the statuses every route shares.
func previewAdminDocumentOp(reg *Registry, method, suffix, id, summary string, status int, errs map[int]string) huma.Operation {
	op := bloemChiDocumentOp(reg, method, "/admin/platform/preview/"+suffix, id, "preview", summary, "bloemPlatformContext", status)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = "Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable."
	if method != "GET" {
		op.Description += " The JSON body is capped at 16 KiB; unknown fields and more than one JSON document are refused."
	}
	op.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &op, bloemRequestDocMerge(map[int]string{
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled.",
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's API key with admin:preview or no scopes, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route.",
		503: "preview_unavailable: preview storage or the server identity is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked.",
	}, errs))
	return op
}

const (
	previewAdminBadBody   = "invalid_request: invalid JSON, unknown fields, more than one JSON document or a body over 16 KiB."
	previewAdminBadIssuer = "invalid_preview_settings: issuer is missing or is not the configured issuer."
	previewAdminNoAccount = "preview_not_configured: ensure the shared preview account first."
)

func registerBloemPreviewAdminDocument(reg *Registry) {
	presentation := previewAdminDocumentOp(reg, "PUT", "accounts/{account_id}/presentation", "setBloemPreviewPersonalPresentation", "Set the personal preview end date for display.", 204, map[int]string{
		400: previewAdminBadBody + " invalid_request is also returned for an account_id that is not a positive integer. invalid_preview_settings: issuer is missing or is not the configured issuer, no issuer is configured yet, the account is the shared preview account, absent or not a current personal preview, or ends_at is outside years 1970 to 9999.",
	})
	presentation.Description += " Only current personal preview accounts can be targeted. This metadata does not expire an account; the storefront owns expiration. A null ends_at clears the date."
	registerBloemChiDocument[BloemPreviewPersonalPresentationInput, struct{}](reg, presentation)
	account := previewAdminDocumentOp(reg, "POST", "account", "ensureBloemPreviewAccount", "Ensure the issuer's shared preview account and primary profile.", 201, map[int]string{
		400: previewAdminBadBody + " invalid_preview_settings: issuer is missing.",
		409: "preview_setup_conflict: issuer differs from the configured issuer, the reserved preview username belongs to another account, or the existing shared account is no longer eligible.",
	})
	account.Description += " Setup is atomic and idempotent. A reserved name collision or second issuer is refused. Password sign-in is disabled and the protected preview template is applied."
	registerBloemChiDocument[BloemPreviewIssuerInput, BloemPreviewAccountOutput](reg, account)
	get := previewAdminDocumentOp(reg, "GET", "settings", "getBloemPreviewSettings", "Read preview settings and public launch keys.", 200, nil)
	registerBloemChiDocument[struct{}, BloemPreviewSettingsOutput](reg, get)
	put := previewAdminDocumentOp(reg, "PUT", "settings", "updateBloemPreviewSettings", "Merge explicitly supplied preview settings.", 204, map[int]string{
		400: previewAdminBadBody + " " + previewAdminBadIssuer + " It is also returned for an invalid host, a link that is not an absolute HTTPS URL without credentials, a session lifetime outside 300 to 86400 seconds, an idle lifetime outside 60 to 7200 seconds or longer than the session, max_concurrent outside 1 to 10000, an unknown maturity_ceiling, overlong text, a host that already serves the application, or enabling without a host and launch key.",
		409: previewAdminNoAccount,
	})
	put.Description += " Account ID and signing keys are read-only here. The existing issuer cannot change. Canonical and served hosts are refused; links require HTTPS without credentials. Turning enabled off revokes guests."
	registerBloemChiDocument[BloemPreviewSettingsInput, struct{}](reg, put)
	libraries := previewAdminDocumentOp(reg, "PUT", "libraries", "setBloemPreviewLibraries", "Revise the preview library selection and reapply it atomically.", 200, map[int]string{
		400: previewAdminBadBody + " " + previewAdminBadIssuer + " It is also returned when library_ids is null. invalid_preview_libraries: more than 1000 IDs, or an ID that is not an existing enabled library.",
		409: previewAdminNoAccount,
	})
	registerBloemChiDocument[BloemPreviewLibrariesInput, BloemPreviewLibrariesOutput](reg, libraries)
	keys := previewAdminDocumentOp(reg, "PUT", "keys", "rotateBloemPreviewKeys", "Replace the Ed25519 launch-key allowlist.", 204, map[int]string{
		400: previewAdminBadBody + " " + previewAdminBadIssuer + " It is also returned unless public_keys holds one or two distinct base64-encoded 32-byte keys.",
		409: previewAdminNoAccount,
	})
	registerBloemChiDocument[BloemPreviewKeysInput, struct{}](reg, keys)
	revoke := previewAdminDocumentOp(reg, "DELETE", "sessions", "revokeBloemPreviewSessions", "Revoke all guest sessions without disabling the preview.", 204, map[int]string{
		400: previewAdminBadBody + " " + previewAdminBadIssuer,
	})
	revoke.Description += " The issuer JSON body is required even for DELETE."
	registerBloemChiDocument[BloemPreviewIssuerInput, struct{}](reg, revoke)
	verify := previewAdminDocumentOp(reg, "POST", "verify", "verifyBloemPreviewTicket", "Verify a freshly signed launch ticket without consuming it.", 200, map[int]string{
		400: previewAdminBadBody,
		403: "preview_ticket_invalid: the ticket is malformed, unsigned by an allowlisted key, expired, or not for this issuer, server and preview host.",
		409: "preview_not_configured: the shared preview account is missing or not ready (default-organization membership, preview template and library selection).",
	})
	verify.Description += " Success always reports valid=true and account_ready=true; refusals use the error statuses."
	registerBloemChiDocument[BloemPreviewVerifyInput, BloemPreviewVerifyOutput](reg, verify)
	capacity := previewAdminDocumentOp(reg, "GET", "capacity", "getBloemPreviewCapacity", "Read private guest capacity and account readiness.", 200, nil)
	registerBloemChiDocument[struct{}, BloemPreviewCapacityOutput](reg, capacity)
	search := previewAdminDocumentOp(reg, "GET", "catalog", "searchBloemPreviewCatalog", "Find metadata visible to the preview for a showcase picker.", 200, map[int]string{
		400: "invalid_preview_settings: query over 256 bytes, provider_id over 128 bytes, an unsupported provider, or only one of provider and provider_id.",
		409: "preview_not_configured: the shared preview account is missing or not ready.",
	})
	search.Description += " Keyword and provider-ID lookup apply the selected enabled libraries and maturity ceiling. Provider and provider_id must appear together. Results are movies, series, audiobooks and ebooks, at most 50. No external calls, file preparation or grants are involved."
	registerBloemChiDocument[BloemPreviewCatalogInput, BloemPreviewCatalogOutput](reg, search)
}

type BloemPreviewPersonalPresentationInput struct {
	AccountID int `path:"account_id" minimum:"1"`
	Body      struct {
		Issuer string     `json:"issuer" minLength:"1" maxLength:"128"`
		EndsAt *time.Time `json:"ends_at" nullable:"true"`
	}
}
