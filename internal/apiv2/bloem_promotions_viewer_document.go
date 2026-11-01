package apiv2

import (
	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// bloemPromotionsViewerAuthority is the native viewer chain the three routes
// share (mountBloemPromotionsViewer), stated once.
const bloemPromotionsViewerAuthority = "Requires authentication, native tenant resolution, viewer/PIN verification and a selected profile, as getBloemSeasonalViewer does; the existing default-deny route boundary excludes direct-profile sessions. Availability is advertised by promotions_viewer_v1 in /api/bloem/v1/capabilities feature_tokens."

type BloemPromotionsViewerHeaders struct {
	ProfileID    string `header:"X-Profile-Id" required:"true" doc:"Verified profile belonging to the authenticated account."`
	ProfileToken string `header:"X-Profile-Token" doc:"PIN verification proof for a locked profile when required by viewer resolution."`
}

type BloemPromotionsListInput struct {
	BloemPromotionsViewerHeaders
	Surface   string `query:"surface" required:"true" enum:"home,detail,pre_playback,in_playback" doc:"The surface to deliver cards for."`
	ContentID string `query:"content_id" doc:"The title the cards are shown with. Required for in_playback; narrows detail and pre_playback; ignored for home."`
}

type BloemPromotionsListOutput struct {
	Body handlers.BloemPromotionsResponse
}

type BloemPromotionSaveInput struct {
	BloemPromotionsViewerHeaders
	ID        string `path:"id" doc:"The campaign ID."`
	ContentID string `query:"content_id" required:"true" doc:"The title being played; the campaign must be a current in_playback card for it."`
}

type BloemPromotionSaveResponse struct {
	Status string `json:"status" enum:"saved"`
}

type BloemPromotionSaveOutput struct {
	Body BloemPromotionSaveResponse
}

type BloemPromotionDismissInput struct {
	BloemPromotionsViewerHeaders
	ID      string `path:"id" doc:"The campaign ID."`
	Surface string `path:"surface" enum:"home,detail,pre_playback,in_playback" doc:"The surface the campaign is no longer shown on."`
}

func registerBloemPromotionsViewerDocument(reg *Registry) {
	viewerErrors := map[int]string{
		403: "viewer_required, profile_unverified or an authority denial: select and verify a profile.",
		404: "not_found: viewer profile is unavailable to this account.",
		503: "unavailable: campaigns are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked.",
	}
	{
		op := bloemChiDocumentOp(reg, "GET", "/promotions", "listBloemPromotions", "engagement", "List the campaign cards a profile sees on a surface.", "bearerAuth", 200)
		op.Description = bloemPromotionsViewerAuthority + " Replaces GET /api/v1/promotions and, for the home surface, the promoted row of the /api/v1 home layout: home cards come with home_position, the row index the campaign row is shown at. Cards the profile dismissed on the surface are left out."
		bloemDocumentErrors[BloemNativeError](reg, &op, viewerErrors)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is required, surface is missing or unknown, or content_id is missing for in_playback.",
			500: "internal_error: campaigns could not be loaded.",
		})
		bloemDocumentRateLimit(reg, &op)
		registerBloemChiDocument[BloemPromotionsListInput, BloemPromotionsListOutput](reg, op)
	}
	{
		op := bloemChiDocumentOp(reg, "POST", "/promotions/{id}/save", "saveBloemPromotion", "engagement", "Save an in-playback campaign to the profile's inbox.", "bearerAuth", 200)
		op.Description = bloemPromotionsViewerAuthority + " Replaces POST /api/v1/promotions/{id}/save. Only a current in_playback card with a call to action for content_id can be saved. The inbox delivery ID is derived from the profile and campaign, so a retry does not add a second copy."
		bloemDocumentErrors[BloemNativeError](reg, &op, viewerErrors)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is required; unavailable: the campaign cannot be saved.",
			404: "not_found: the campaign is no longer available to this profile, or the viewer profile is unavailable.",
			500: "internal_error: the campaign could not be saved.",
			503: "unavailable: campaigns or the inbox are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked.",
		})
		bloemDocumentRateLimit(reg, &op)
		registerBloemChiDocument[BloemPromotionSaveInput, BloemPromotionSaveOutput](reg, op)
	}
	{
		op := bloemChiDocumentOp(reg, "PUT", "/promotions/{id}/dismissals/{surface}", "dismissBloemPromotion", "engagement", "Stop showing a campaign to a profile on a surface.", "bearerAuth", 204)
		op.Description = bloemPromotionsViewerAuthority + " Replaces PUT /api/v1/home/dismissals/promo:{surface}/{id}, and writes the same per-profile dismissal. Idempotent: repeating it refreshes the dismissal."
		bloemDocumentErrors[BloemNativeError](reg, &op, viewerErrors)
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: X-Profile-Id is required, the surface is unknown, or the campaign ID is longer than 64 bytes.",
			500: "internal_error: the dismissal could not be saved.",
			503: "unavailable: campaigns or dismissals are not available; service_unavailable or tenant_unavailable: the session, tenant or viewer access could not be checked.",
		})
		bloemDocumentRateLimit(reg, &op)
		registerBloemChiDocument[BloemPromotionDismissInput, struct{}](reg, op)
	}
}
