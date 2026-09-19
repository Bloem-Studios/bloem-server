package apiv2

import (
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// Inputs preserve the handlers' defaults and optional fields. The service
// structs deliberately lack omitempty, so using them as request schemas would
// incorrectly require every field, including fields with documented defaults.
// Responses reuse the actual domain types sent by the handlers.
type BloemPromotionBody struct {
	_              struct{}                `additionalProperties:"true"`
	OrganizationID *uuid.UUID              `json:"organization_id,omitempty" nullable:"true" doc:"Null or omitted means deployment-wide; otherwise the campaign belongs to this organization."`
	Surfaces       []string                `json:"surfaces" minItems:"1" nullable:"false" doc:"One or more of home, detail, pre_playback, in_playback; normalized to lowercase and deduplicated."`
	Placement      promotions.Placement    `json:"placement,omitempty" doc:"in_playback requires playback_style card or pip and dismissible=true. Overlay duration defaults to 10 seconds and must be 5-60; video_url requires pip and HTTPS. Detail/pre-playback content_ids are limited to 64."`
	Kicker         string                  `json:"kicker,omitempty" doc:"Trimmed text, at most 40 bytes."`
	Headline       string                  `json:"headline" minLength:"1" doc:"Required nonblank trimmed text, at most 120 bytes."`
	Subtitle       string                  `json:"subtitle,omitempty" doc:"Trimmed text, at most 200 bytes."`
	ImageURL       string                  `json:"image_url" minLength:"1" doc:"HTTPS URL or a server ambience asset path."`
	ImageWidth     *int                    `json:"image_width,omitempty" nullable:"true" minimum:"1" doc:"Supply with image_height; declared artwork must be 16:9 within one percent."`
	ImageHeight    *int                    `json:"image_height,omitempty" nullable:"true" minimum:"1"`
	Deeplink       string                  `json:"deeplink,omitempty" doc:"HTTPS URL, bloem:// deeplink or app path."`
	CTA            *promotions.CTA         `json:"cta,omitempty" doc:"Optional button with a nonblank label of at most 40 bytes and an HTTPS URL, bloem:// deeplink or app path."`
	Priority       int                     `json:"priority,omitempty" default:"0"`
	StartsAt       time.Time               `json:"starts_at" doc:"Required start instant, strictly before ends_at."`
	EndsAt         time.Time               `json:"ends_at"`
	Targeting      BloemPromotionTargeting `json:"targeting,omitempty" doc:"Defaults to all; fields outside the chosen audience are discarded."`
	Dismissible    *bool                   `json:"dismissible,omitempty" nullable:"true" doc:"Defaults to true when omitted or null. Must be true for in_playback."`
}

type BloemPromotionTargeting struct {
	_              struct{} `additionalProperties:"true"`
	Audience       string   `json:"audience,omitempty" doc:"all (default), role, organization, library or explicit; normalized to lowercase."`
	Role           string   `json:"role,omitempty" doc:"For role targeting: admin or user."`
	OrganizationID string   `json:"organization_id,omitempty" doc:"For organization targeting: an organization UUID."`
	LibraryID      int      `json:"library_id,omitempty" doc:"For library targeting: a positive library id."`
	UserIDs        []int    `json:"user_ids,omitempty" doc:"For explicit targeting: account ids; at least one account or profile is required."`
	ProfileIDs     []string `json:"profile_ids,omitempty"`
}

type BloemPromotionCreateInput struct{ Body BloemPromotionBody }
type BloemPromotionUpdateInput struct {
	ID   string `path:"id" doc:"Stored campaign identity."`
	Body BloemPromotionBody
}
type BloemEngagementItemInput struct {
	ID string `path:"id" doc:"Stored registry identity."`
}
type BloemPromotionOutput struct{ Body promotions.Promotion }
type BloemPromotionList struct {
	Promotions []promotions.Promotion `json:"promotions" nullable:"false"`
	Surfaces   []string               `json:"surfaces" nullable:"false" doc:"Supported delivery surfaces: home, detail, pre_playback, in_playback."`
}
type BloemPromotionListOutput struct{ Body BloemPromotionList }

type BloemAmbienceBody struct {
	_              struct{}        `additionalProperties:"true"`
	EffectID       string          `json:"effect_id" pattern:"^[a-z0-9][a-z0-9_-]{0,63}$" doc:"Required lowercase effect slug; surrounding whitespace is trimmed."`
	Window         ambience.Window `json:"window" doc:"starts_at must precede ends_at. timezone defaults to UTC and must be an IANA zone; a repeat_yearly window must be shorter than one year in that zone."`
	Intensity      *float64        `json:"intensity,omitempty" nullable:"true" minimum:"0" maximum:"1" doc:"Defaults to 1 when omitted or null."`
	Surfaces       []string        `json:"surfaces,omitempty" doc:"all, home or login; omitted or empty defaults to all. Values are normalized to lowercase and all absorbs other surfaces."`
	Assets         ambience.Assets `json:"assets,omitempty" doc:"Optional banner and at most 32 sprites, each an HTTPS URL or a server ambience asset path."`
	OrganizationID *uuid.UUID      `json:"organization_id,omitempty" nullable:"true" doc:"Null or omitted means deployment-wide; otherwise this pack belongs to the organization."`
}
type BloemAmbienceCreateInput struct{ Body BloemAmbienceBody }
type BloemAmbienceUpdateInput struct {
	ID   string `path:"id" doc:"Stored ambience pack identity."`
	Body BloemAmbienceBody
}
type BloemAmbienceOutput struct{ Body ambience.Pack }
type BloemAmbienceList struct {
	Packs            []ambience.Pack `json:"packs" nullable:"false"`
	StorageAvailable bool            `json:"storage_available" doc:"Whether server asset upload storage is configured."`
	YearlyScheduling bool            `json:"yearly_scheduling" doc:"True: yearly recurring windows are supported."`
}
type BloemAmbienceListOutput struct{ Body BloemAmbienceList }

type BloemAmbienceUploadForm struct {
	File        huma.FormFile `form:"file" required:"true" contentType:"image/png,image/webp,image/jpeg,image/gif" doc:"Image bytes, at most 8 MiB. The server sniffs the actual image type."`
	AssetID     string        `form:"asset_id" required:"false" maxLength:"128" doc:"Optional stable authoring identity, trimmed. Reusing it upserts the stored asset; identical bytes return the same content-addressed object."`
	Kind        string        `form:"kind" required:"false" enum:"campaign_card_16x9,season_banner,season_sprite" doc:"Optional authoring asset kind, trimmed."`
	Checksum    string        `form:"checksum" required:"false" doc:"Optional SHA-256 hex digest; compared case-insensitively to the uploaded bytes."`
	ContentType string        `form:"content_type" required:"false" doc:"Accepted but ignored: the server sniffs the bytes."`
}
type BloemAmbienceUploadInput struct {
	RawBody huma.MultipartFormFiles[BloemAmbienceUploadForm]
}
type BloemAmbienceUploadResponse struct {
	URL   string               `json:"url"`
	Asset ambience.StoredAsset `json:"asset"`
}
type BloemAmbienceUploadOutput struct{ Body BloemAmbienceUploadResponse }

type BloemAmbienceAttachForm struct {
	File huma.FormFile `form:"file" required:"true" contentType:"image/png,image/webp,image/jpeg,image/gif" doc:"Image bytes, at most 8 MiB. The server sniffs the actual image type."`
	Slot string        `form:"slot" required:"false" enum:"banner,sprite" default:"banner" doc:"banner replaces the banner URL; sprite appends one sprite, up to 32."`
}
type BloemAmbienceAttachInput struct {
	ID      string `path:"id" doc:"Stored ambience pack identity."`
	RawBody huma.MultipartFormFiles[BloemAmbienceAttachForm]
}
type BloemAmbienceAttachResponse struct {
	URL  string        `json:"url"`
	Slot string        `json:"slot" enum:"banner,sprite"`
	Pack ambience.Pack `json:"pack"`
}
type BloemAmbienceAttachOutput struct{ Body BloemAmbienceAttachResponse }

func bloemEngagementDocumentOp(reg *Registry, method, path, id, summary string, status int) huma.Operation {
	op := bloemChiDocumentOp(reg, method, "/admin/platform"+path, id, "engagement", summary, "bloemPlatformContext", status)
	op.Description = "Requires a platform-scoped administrative-context token, with current account and originating login-session authority revalidated. Organization contexts and ordinary account tokens are insufficient. This registry can author both deployment-wide and organization-owned content; organization_id in a body selects ownership, not authority."
	bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{500: "internal_error: the registry operation failed."})
	return op
}

func registerBloemEngagementDocument(reg *Registry) {
	listPromotions := bloemEngagementDocumentOp(reg, "GET", "/promotions/", "listBloemPlatformPromotions", "List platform-managed campaigns and supported delivery surfaces.", 200)
	registerBloemChiDocument[struct{}, BloemPromotionListOutput](reg, listPromotions)
	listPacks := bloemEngagementDocumentOp(reg, "GET", "/ambience/", "listBloemPlatformAmbience", "List ambience packs and authoring capabilities.", 200)
	registerBloemChiDocument[struct{}, BloemAmbienceListOutput](reg, listPacks)
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		path, promoID, packID, summary, status := "/", "createBloemPlatformPromotion", "createBloemPlatformAmbience", "Create a registry entry.", 201
		switch method {
		case "PUT":
			path, promoID, packID, summary, status = "/{id}", "updateBloemPlatformPromotion", "updateBloemPlatformAmbience", "Replace every editable field of a registry entry.", 200
		case "DELETE":
			path, promoID, packID, summary, status = "/{id}", "deleteBloemPlatformPromotion", "deleteBloemPlatformAmbience", "Delete a registry entry.", 204
		}
		promo := bloemEngagementDocumentOp(reg, method, "/promotions"+path, promoID, summary, status)
		pack := bloemEngagementDocumentOp(reg, method, "/ambience"+path, packID, summary, status)
		for _, op := range []*huma.Operation{&promo, &pack} {
			if method != "DELETE" {
				op.Description += " JSON body limit: 64 KiB. PUT is a full replacement, applying defaults to omitted optional fields. These writes have no expected_revision, If-Match or replay receipt; reconcile the list after an uncertain result before submitting again."
				bloemDocumentErrors[BloemNativeError](reg, op, map[int]string{400: "bad_request: invalid JSON or domain validation failure; the message identifies the invalid field."})
			}
			if method != "POST" {
				bloemDocumentErrors[BloemNativeError](reg, op, map[int]string{404: "not_found: registry entry does not exist (including a repeated deletion)."})
			}
		}
		switch method {
		case "POST":
			registerBloemChiDocument[BloemPromotionCreateInput, BloemPromotionOutput](reg, promo)
			registerBloemChiDocument[BloemAmbienceCreateInput, BloemAmbienceOutput](reg, pack)
		case "PUT":
			registerBloemChiDocument[BloemPromotionUpdateInput, BloemPromotionOutput](reg, promo)
			registerBloemChiDocument[BloemAmbienceUpdateInput, BloemAmbienceOutput](reg, pack)
		case "DELETE":
			pack.Description += " Stored artwork objects are left in place."
			registerBloemChiDocument[BloemEngagementItemInput, struct{}](reg, promo)
			registerBloemChiDocument[BloemEngagementItemInput, struct{}](reg, pack)
		}
	}
	for _, attach := range []bool{false, true} {
		path, id, summary := "/ambience/assets", "uploadBloemPlatformAmbienceAsset", "Upload a standalone authoring asset without attaching it to a pack."
		if attach {
			path, id, summary = "/ambience/{id}/assets", "attachBloemPlatformAmbienceAsset", "Upload artwork and attach it to an ambience pack."
		}
		op := bloemEngagementDocumentOp(reg, "POST", path, id, summary, 201)
		op.Description += " Requires configured asset storage. Send multipart/form-data with a file part of at most 8 MiB; the entire form is capped at 9 MiB. PNG, WebP, JPEG and GIF are accepted by sniffing the bytes, regardless of declared content type. The response includes the public asset URL."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "bad_request: invalid multipart form, missing file, checksum mismatch or invalid asset metadata/slot.",
			413: "too_large: artwork or the multipart body exceeds its limit.",
			415: "unsupported_image: bytes are not a supported raster image.",
		})
		if attach {
			op.Description += " The default slot is banner; sprite appends to the pack. Do not automatically retry sprite attachment: it can append twice."
			bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{404: "not_found: ambience pack does not exist."})
			registerBloemChiDocument[BloemAmbienceAttachInput, BloemAmbienceAttachOutput](reg, op)
		} else {
			op.Description += " asset_id is optional. Reusing it with identical bytes returns the existing asset; different bytes replace its recorded reference."
			registerBloemChiDocument[BloemAmbienceUploadInput, BloemAmbienceUploadOutput](reg, op)
		}
	}
	bloemEngagementDocumentNullability(reg.api.OpenAPI().Components.Schemas)
}
