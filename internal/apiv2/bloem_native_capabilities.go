package apiv2

import (
	"github.com/danielgtaylor/huma/v2"
)

// BloemCapabilityFeatures is the fixed object of booleans the native capability
// probe advertises. It mirrors the wire shape the chi handler already serves
// (handlers.bloemCapabilityFeatures); the two are separate declarations because
// this package must not import handlers, and
// TestBloemCapabilityDocumentMatchesTheServedShape holds them together.
type BloemCapabilityFeatures struct {
	LegacySiloV1            bool `json:"legacy_silo_v1" doc:"This build still answers Silo's frozen /api/v1 surface."`
	OrganizationMemberships bool `json:"organization_memberships" doc:"Accounts can belong to organizations."`
	TenantBoundedMediaScope bool `json:"tenant_bounded_media_scope" doc:"Media visibility is bounded by the viewer's organization."`
	DirectProfileLogin      bool `json:"direct_profile_login" doc:"A profile can hold its own credentials rather than logging in through its account."`
	SharedDevicePairing     bool `json:"shared_device_pairing" doc:"A device can be paired to a household rather than to one account."`
	DelegatedAdminRoles     bool `json:"delegated_admin_roles" doc:"Administration can be delegated below the server owner."`
}

// BloemCapabilities is the native surface's capability probe response.
type BloemCapabilities struct {
	API            string                  `json:"api" doc:"Identifier of the native surface this build serves." example:"bloem/v1"`
	IdentitySchema int                     `json:"identity_schema" doc:"Version of the server/organization/account/profile identity model."`
	Features       BloemCapabilityFeatures `json:"features"`
	MediaTypes     []string                `json:"media_types" doc:"The item types this build can serve."`
	// FeatureTokens is the allowlist clients match against. It is separate from
	// Features because Features is a fixed object of booleans on the wire, and
	// versioned tokens (watch_document_v1) cannot be expressed as stable field
	// names.
	FeatureTokens []string `json:"feature_tokens" doc:"Versioned capability tokens a client matches against. Some depend on what this deployment has configured; ignore tokens you do not recognize."`
}

// BloemCapabilitiesOutput is the envelope for the probe.
type BloemCapabilitiesOutput struct {
	Body BloemCapabilities
}

// registerBloemCapabilities documents the native capability probe.
//
// The route is served by chi (handlers.BloemSystemHandler.HandleCapabilities)
// and mounted unconditionally. The probe is unauthenticated and takes
// nothing, by design: a probe that can itself fail or be refused leaves a
// client interpreting exactly the ambiguity the probe exists to replace. So
// the document declares the one response the handler writes, with no
// authentication, rate limit or v2 gate statuses.
func registerBloemCapabilities(reg *Registry) {
	op := bloemOp("GET", "/capabilities", "getBloemCapabilities", "system",
		"What this build supports, for a client deciding which surfaces to use.")
	op.Security = []map[string][]string{}
	op.Description = "Public and always answers 200; no authentication, rate limit or tenant gate runs in front of it."
	op.Responses = map[string]*huma.Response{}
	registerBloemChiDocument[struct{}, BloemCapabilitiesOutput](reg, op)
	// Huma adds a generic "default" error response to an operation that
	// declares no error status. This probe has none to declare.
	delete(reg.api.OpenAPI().Paths[op.Path].Get.Responses, "default")
}
