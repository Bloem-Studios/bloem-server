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

func previewAdminDocumentOp(reg *Registry, method, suffix, id, summary string, status int) huma.Operation {
	op := bloemChiDocumentOp(reg, method, "/admin/platform/preview/"+suffix, id, "preview", summary, "bloemPlatformContext", status)
	op.Security = append(op.Security, map[string][]string{"bearerAuth": {}})
	op.Description = "Requires a Platform administrative context or an enabled administrator's API key with admin:preview. Unscoped admin keys retain access. Organization contexts and unrelated scopes are refused. Responses are not cacheable. JSON bodies are capped at 16 KiB and unknown fields are refused."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{400: "Invalid preview configuration or payload.", 403: "insufficient_platform_authority or preview_ticket_invalid.", 409: "preview_setup_conflict or preview_not_configured.", 503: "preview_unavailable: storage or authority unavailable."})
	return op
}

func registerBloemPreviewAdminDocument(reg *Registry) {
	account := previewAdminDocumentOp(reg, "POST", "account", "ensureBloemPreviewAccount", "Ensure the issuer's shared preview account and primary profile.", 201)
	account.Description += " Setup is atomic and idempotent. A reserved name collision or second issuer is refused. Password sign-in is disabled and the protected preview template is applied."
	registerBloemChiDocument[BloemPreviewIssuerInput, BloemPreviewAccountOutput](reg, account)
	get := previewAdminDocumentOp(reg, "GET", "settings", "getBloemPreviewSettings", "Read preview settings and public launch keys.", 200)
	registerBloemChiDocument[struct{}, BloemPreviewSettingsOutput](reg, get)
	put := previewAdminDocumentOp(reg, "PUT", "settings", "updateBloemPreviewSettings", "Merge explicitly supplied preview settings.", 204)
	put.Description += " Account ID and signing keys are read-only here. The existing issuer cannot change. Canonical and served hosts are refused; links require HTTPS without credentials. Turning enabled off revokes guests."
	registerBloemChiDocument[BloemPreviewSettingsInput, struct{}](reg, put)
	libraries := previewAdminDocumentOp(reg, "PUT", "libraries", "setBloemPreviewLibraries", "Revise the preview library selection and reapply it atomically.", 200)
	registerBloemChiDocument[BloemPreviewLibrariesInput, BloemPreviewLibrariesOutput](reg, libraries)
	keys := previewAdminDocumentOp(reg, "PUT", "keys", "rotateBloemPreviewKeys", "Replace the Ed25519 launch-key allowlist.", 204)
	registerBloemChiDocument[BloemPreviewKeysInput, struct{}](reg, keys)
	revoke := previewAdminDocumentOp(reg, "DELETE", "sessions", "revokeBloemPreviewSessions", "Revoke all guest sessions without disabling the preview.", 204)
	revoke.Description += " The issuer JSON body is required even for DELETE."
	registerBloemChiDocument[BloemPreviewIssuerInput, struct{}](reg, revoke)
	verify := previewAdminDocumentOp(reg, "POST", "verify", "verifyBloemPreviewTicket", "Verify a freshly signed launch ticket without consuming it.", 200)
	registerBloemChiDocument[BloemPreviewVerifyInput, BloemPreviewVerifyOutput](reg, verify)
	capacity := previewAdminDocumentOp(reg, "GET", "capacity", "getBloemPreviewCapacity", "Read private guest capacity and account readiness.", 200)
	registerBloemChiDocument[struct{}, BloemPreviewCapacityOutput](reg, capacity)
	search := previewAdminDocumentOp(reg, "GET", "catalog", "searchBloemPreviewCatalog", "Find metadata visible to the preview for a showcase picker.", 200)
	search.Description += " Keyword and provider-ID lookup apply the selected enabled libraries and maturity ceiling. Provider and provider_id must appear together. No external calls, file preparation or grants are involved."
	registerBloemChiDocument[BloemPreviewCatalogInput, BloemPreviewCatalogOutput](reg, search)
}
