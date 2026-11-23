package handlers

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

type BloemOrganizationStore interface {
	ListMemberships(context.Context, int) ([]tenancy.Membership, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type BloemSystemHandler struct {
	organizations              BloemOrganizationStore
	seasonalViewer             bool
	playbackOverlays           bool
	promotionsViewer           bool
	organizationDomains        bool
	organizationSupport        bool
	organizationLive           bool
	bookRequests               bool
	ebookRequests              bool
	bookRequestConnectionAdmin bool
	nativeAudioReadiness       func(context.Context) bool
	// adminPushReady reports whether the admin push routes are mounted and the
	// operator has push delivery on. Nil means the routes are not mounted.
	adminPushReady func(context.Context) bool
	// adminLiveReady reports whether the operator has admin Live Activities on.
	adminLiveReady func(context.Context) bool
}

func NewBloemSystemHandler(organizations BloemOrganizationStore) *BloemSystemHandler {
	return &BloemSystemHandler{organizations: organizations}
}

func (h *BloemSystemHandler) SetBookRequestsAvailable(available bool) { h.bookRequests = available }

func (h *BloemSystemHandler) SetEbookRequestsAvailable(available bool) { h.ebookRequests = available }

// SetBookRequestConnectionAdminAvailable tracks the admin-context connection
// edit and test routes of the book request lanes.
func (h *BloemSystemHandler) SetBookRequestConnectionAdminAvailable(available bool) {
	h.bookRequestConnectionAdmin = available
}

// SetPlaybackOverlaysAvailable tracks the campaign registry that in-playback
// cards need; a storefront checks it before saving one.
func (h *BloemSystemHandler) SetPlaybackOverlaysAvailable(available bool) {
	h.playbackOverlays = available
}

// SetPromotionsViewerAvailable tracks the native viewer campaign routes
// (/promotions, /promotions/{id}/save, /promotions/{id}/dismissals/{surface}).
func (h *BloemSystemHandler) SetPromotionsViewerAvailable(available bool) {
	h.promotionsViewer = available
}

// SetOrganizationDomainsAvailable tracks the platform routes for an
// organization's domains and branding (/admin/platform/tenants/{id}/domains
// and /branding).
func (h *BloemSystemHandler) SetOrganizationDomainsAvailable(available bool) {
	h.organizationDomains = available
}

// SetOrganizationSupportAvailable tracks the organization support routes: a
// member's password reset, sessions, devices and profiles, and the
// organization's streams (/admin/organization/support-capabilities says which
// parts this server can serve).
func (h *BloemSystemHandler) SetOrganizationSupportAvailable(available bool) {
	h.organizationSupport = available
}

// SetOrganizationLiveAvailable tracks the organization live updates: the
// ticket route and the socket that tell an organization administrator's app
// which streams, people or activity entries changed.
func (h *BloemSystemHandler) SetOrganizationLiveAvailable(available bool) {
	h.organizationLive = available
}

// SetSeasonalViewerAvailable tracks the authenticated native ambience route.
func (h *BloemSystemHandler) SetSeasonalViewerAvailable(available bool) {
	h.seasonalViewer = available
}

// Aggregate feature tokens. Each names a client-visible capability of this
// build; the per-subsystem capability endpoints (/playback/capability,
// /events/capability, /auth/device/capability, …) stay the place to look for
// the details of one. Clients hold an allowlist of tokens they understand and
// ignore the rest, so adding one here is additive by construction.
const (
	// featureWatchDocumentV1 covers the versioned Watch documents served at
	// GET /api/bloem/v2/watch/home and GET /api/bloem/v2/watch/items/{content_id}.
	featureWatchDocumentV1 = "watch_document_v1"
	// featureDevicePairingV1 covers pairing a television against an account
	// without typing a password on it. Protocol details live on
	// /auth/device/capability.
	featureDevicePairingV1 = "device_pairing_v1"
	// featureHouseholdLoginsV1 covers household logins: accounts of their own
	// that belong to a main account, created and listed under
	// /api/bloem/v2/admin/platform/accounts/{account_id}/logins.
	featureHouseholdLoginsV1 = "household_logins_v1"
	// featureHouseholdPoolV1 covers a household's shared pool of streams and
	// transcodes, set on the main account under
	// /api/bloem/v2/admin/platform/accounts/{account_id}/household-pool.
	featureHouseholdPoolV1 = "household_pool_v1"
	// featureHouseholdLoginMovesV1 covers moving a person between a profile of
	// a main account and a household login of their own:
	// .../accounts/{account_id}/logins/move-profile and
	// .../logins/{login_id}/move-to-main.
	featureHouseholdLoginMovesV1 = "household_login_moves_v1"
	// featureHouseholdLoginCapV1 covers a household's login cap, set on the
	// main account under .../accounts/{account_id}/household-login-cap: a new
	// login past it is refused with household_login_cap_reached.
	featureHouseholdLoginCapV1 = "household_login_cap_v1"
	// featureDeclaredEventChannels is the aggregate name for the events
	// websocket's declared-channel handshake, reported in detail as
	// declared_channels on /events/capability.
	featureDeclaredEventChannels = "declared_event_channels"
)

// mediaTypesServed are the item types this build can serve to clients — a
// property of the software, not of what the operator happens to have in their
// libraries. The vocabulary is media_items.type, the same set
// catalog.IsValidMediaScope accepts, minus its "video" grouping scope, which is
// an internal scope expanding to movie and series rather than a media type a
// client can be handed.
//
// A type absent here means "this build cannot serve it", which is exactly how a
// client reads an unknown or missing entry.
var mediaTypesServed = []string{
	itemTypeMovie,
	itemTypeSeries,
	itemTypeEpisode,
	itemTypeAudiobook,
	itemTypeEbook,
	itemTypeManga,
}

// bloemCapabilityTokens is assembled once: the document describes the build, not
// the request or the caller. Playback contributes the same tokens it publishes
// on /playback/capability, taken from the function that owns them so the
// aggregate cannot drift away from the endpoint it summarizes.
var bloemCapabilityTokens = buildBloemCapabilityTokens()

func buildBloemCapabilityTokens() []string {
	playbackFeatures := playback.ServerFeaturesV3()
	tokens := make([]string, 0, len(playbackFeatures)+4)
	add := func(candidates ...string) {
		for _, token := range candidates {
			if !slices.Contains(tokens, token) {
				tokens = append(tokens, token)
			}
		}
	}
	add(playbackFeatures...)
	add(featureDeclaredEventChannels, "live_tv_access_v1", "organization_activity_v1", "organization_invitation_lifecycle_v1", "organization_invitation_links_v1", "platform_engagement_authoring_v1")
	add(featureWatchDocumentV1, featureDevicePairingV1, featureHouseholdLoginsV1, featureHouseholdPoolV1, featureHouseholdLoginMovesV1, featureHouseholdLoginCapV1)
	return tokens
}

// bloemCapabilitiesResponse is the public, unauthenticated answer to "what does
// this server do". Fields are additive-only: media_types and feature_tokens
// were added beside the original three without touching them, and the token
// list is where every later capability lands.
// nativeAPIIdentifier names the surface this document describes. It matches the
// segment the surface is mounted at, so it moves with the path rather than
// standing as a second, independently drifting version number.
const nativeAPIIdentifier = "bloem/v2"

type bloemCapabilitiesResponse struct {
	API            string                  `json:"api"`
	IdentitySchema int                     `json:"identity_schema"`
	Features       bloemCapabilityFeatures `json:"features"`
	// MediaTypes are the item types this build can serve.
	MediaTypes []string `json:"media_types"`
	// FeatureTokens is the token allowlist clients match against. It is a
	// separate field rather than an addition to Features because Features is a
	// fixed object of booleans on the wire already, and versioned tokens
	// (watch_document_v1) cannot be expressed as stable field names.
	FeatureTokens []string `json:"feature_tokens"`
}

type bloemCapabilityFeatures struct {
	LegacySiloV1            bool `json:"legacy_silo_v1"`
	OrganizationMemberships bool `json:"organization_memberships"`
	TenantBoundedMediaScope bool `json:"tenant_bounded_media_scope"`
	DirectProfileLogin      bool `json:"direct_profile_login"`
	SharedDevicePairing     bool `json:"shared_device_pairing"`
	DelegatedAdminRoles     bool `json:"delegated_admin_roles"`
}

// HandleCapabilities answers the public capability probe. It is
// unauthenticated and never fails: a probe that can itself be unavailable
// leaves a client interpreting the same ambiguous status the probe exists to
// replace. The slices are cloned so no caller can mutate the shared
// advertisement.
func (h *BloemSystemHandler) HandleCapabilities(w http.ResponseWriter, request *http.Request) {
	featureTokens := slices.Clone(bloemCapabilityTokens)
	if h.nativeAudioReadiness != nil && h.nativeAudioReadiness(request.Context()) {
		featureTokens = append(featureTokens, "native_audiobook_presentations_v1")
	}
	if h.bookRequests {
		featureTokens = append(featureTokens, "audiobook_requests_v1")
	}
	if h.ebookRequests {
		featureTokens = append(featureTokens, "ebook_requests_v1")
	}
	if h.bookRequestConnectionAdmin {
		featureTokens = append(featureTokens, "book_request_connection_admin_v1")
	}
	if h.seasonalViewer {
		featureTokens = append(featureTokens, "seasonal_viewer_v1")
	}
	if h.playbackOverlays {
		featureTokens = append(featureTokens, "playback_overlays_v1")
	}
	if h.promotionsViewer {
		featureTokens = append(featureTokens, "promotions_viewer_v1")
	}
	if h.organizationDomains {
		featureTokens = append(featureTokens, "organization_domains_v1")
	}
	if h.organizationSupport {
		featureTokens = append(featureTokens, "organization_support_v1")
	}
	if h.organizationLive {
		featureTokens = append(featureTokens, "organization_live_v1")
	}
	if h.adminPushReady != nil && h.adminPushReady(request.Context()) {
		// The admin apps show their push settings only on a server that says so.
		featureTokens = append(featureTokens, notifications.AdminPushCapabilityToken, notifications.AdminWidgetWakesCapabilityToken,
			notifications.AdminWidgetHealthCapabilityToken)
		if h.adminLiveReady != nil && h.adminLiveReady(request.Context()) {
			featureTokens = append(featureTokens, notifications.AdminLiveActivitiesCapabilityToken)
		}
	}
	writeJSON(w, http.StatusOK, bloemCapabilitiesResponse{
		API:            nativeAPIIdentifier,
		IdentitySchema: 1,
		Features: bloemCapabilityFeatures{
			LegacySiloV1:            false, // Silo retired its alpha v1 API (Silo #3200)
			OrganizationMemberships: true,
			TenantBoundedMediaScope: true,
			DirectProfileLogin:      false, // profile sign-in was removed
		},
		MediaTypes:    slices.Clone(mediaTypesServed),
		FeatureTokens: featureTokens,
	})
}

type bloemOrganizationListResponse struct {
	Organizations []bloemOrganization `json:"organizations"`
}

type bloemOrganization struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Default          bool   `json:"default"`
	MembershipID     string `json:"membership_id"`
	MembershipRole   string `json:"membership_role"`
	PolicyRevision   int64  `json:"policy_revision"`
	SecurityRevision int64  `json:"security_revision"`
}

func (h *BloemSystemHandler) HandleOrganizations(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID <= 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if h == nil || h.organizations == nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}

	memberships, err := h.organizations.ListMemberships(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}

	result := make([]bloemOrganization, 0, len(memberships))
	for _, membership := range memberships {
		if membership.AccountID != claims.UserID || membership.Status != tenancy.MembershipActive {
			continue
		}
		organization, err := h.organizations.GetOrganization(r.Context(), membership.OrganizationID)
		if errors.Is(err, tenancy.ErrOrganizationNotFound) {
			continue
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
			return
		}
		if organization.Status != tenancy.OrganizationActive || organization.OwnerAccountID == nil || organization.ID != membership.OrganizationID {
			continue
		}
		result = append(result, bloemOrganization{
			ID:               organization.ID.String(),
			Slug:             organization.Slug,
			Name:             organization.Name,
			Default:          organization.Default,
			MembershipID:     membership.ID.String(),
			MembershipRole:   membership.LegacyRole,
			PolicyRevision:   organization.PolicyRevision,
			SecurityRevision: membership.SecurityRevision,
		})
	}

	writeJSON(w, http.StatusOK, bloemOrganizationListResponse{Organizations: result})
}

// SetAdminPushReadiness advertises admin_push_v1 while ready reports true. It is
// asked on every probe, so an operator switching push delivery off withdraws
// the token at once. Without it the token is never advertised.
func (h *BloemSystemHandler) SetAdminPushReadiness(ready func(context.Context) bool) {
	h.adminPushReady = ready
}

// SetAdminLiveActivitiesReadiness advertises admin_live_activities_v1 while ready
// reports true, and only alongside admin_push_v1. It is asked on every probe, so
// the operator's switch takes effect at once.
func (h *BloemSystemHandler) SetAdminLiveActivitiesReadiness(ready func(context.Context) bool) {
	h.adminLiveReady = ready
}

// SetNativeAudioReadiness advertises only the composed native audio service.
func (h *BloemSystemHandler) SetNativeAudioReadiness(ready func(context.Context) bool) {
	h.nativeAudioReadiness = ready
}
