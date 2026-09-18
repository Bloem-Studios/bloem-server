package apiv2

import "github.com/Silo-Server/silo-server/internal/ambience"

type BloemSeasonalViewerInput struct {
	ProfileID    string `header:"X-Profile-Id" required:"true" doc:"Verified profile belonging to the authenticated account and the resolved active organization."`
	ProfileToken string `header:"X-Profile-Token" doc:"PIN verification proof for a locked profile when required by viewer resolution."`
}

type BloemSeasonalViewerResponse struct {
	Ambience []ambience.Wire `json:"ambience" nullable:"false" doc:"Currently active deployment-wide packs plus packs for the resolved organization. Empty when none apply; entries omit administrative ownership and audit fields."`
}

type BloemSeasonalViewerOutput struct {
	CacheControl string `header:"Cache-Control" doc:"private, no-store"`
	Body         BloemSeasonalViewerResponse
}

func registerBloemSeasonalViewerDocument(reg *Registry) {
	op := bloemChiDocumentOp(reg, "GET", "/ambience", "getBloemSeasonalViewer", "engagement", "Read seasonal presentation for the current verified tenant viewer.", "bearerAuth", 200)
	op.Description = "Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile. Account, profile, active organization and membership must agree; policy and security revisions must be positive. Explicit tenant claims are revalidated; legacy account sessions resolve to the default organization, not an organization selected by a header. The profile's organization is checked before delivering public and organization-owned packs. The existing default-deny route boundary excludes direct-profile sessions. This is private viewer delivery, separate from public login branding. Handler responses use Cache-Control: private, no-store. Availability is advertised by seasonal_viewer_v1 in /api/bloem/v1/capabilities feature_tokens."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
		400: "bad_request: X-Profile-Id is required.",
		403: "viewer_required, profile_unverified or an authority denial: select and verify a profile in an active organization.",
		404: "not_found: viewer profile is unavailable to this account.",
		500: "internal_error: viewer access could not be resolved.",
	})
	bloemDocumentRateLimit(reg, &op)
	registerBloemChiDocument[BloemSeasonalViewerInput, BloemSeasonalViewerOutput](reg, op)
}
