package apiv2

import (
	"context"
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
	FeatureTokens []string `json:"feature_tokens" doc:"Versioned capability tokens a client matches against."`
}

// BloemCapabilitiesOutput is the huma envelope for the probe.
type BloemCapabilitiesOutput struct {
	Body BloemCapabilities
}

// BloemCapabilitiesInput carries no parameters: the probe is unauthenticated
// and takes nothing, by design. A probe that can itself fail or be refused
// leaves a client interpreting exactly the ambiguity the probe exists to
// replace.
type BloemCapabilitiesInput struct{}

// registerBloemCapabilities documents the native capability probe.
//
// The route is still served by chi (handlers.BloemSystemHandler.HandleCapabilities).
// Registering it here does not move it; it puts the operation in the committed
// document so the surface has a machine-readable contract, and
// bloem_native_document_test.go proves every documented path is one the router
// actually mounts. Moving the serving to huma is a per-route step that follows.
func registerBloemCapabilities(reg *Registry) {
	op := Operation{
		Operation: bloemOp(
			"GET",
			"/capabilities",
			"getBloemCapabilities",
			"system",
			"What this build supports, for a client deciding which surfaces to use.",
		),
		Class: ClassPublic,
	}
	Register(reg, op, func(context.Context, *BloemCapabilitiesInput) (*BloemCapabilitiesOutput, error) {
		// Unreachable during document generation, which uses noopAdapter. It
		// becomes the live handler when /capabilities moves off chi; until
		// then the chi handler is the one that answers.
		return &BloemCapabilitiesOutput{}, nil
	})
}
