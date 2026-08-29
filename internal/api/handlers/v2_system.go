package handlers

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

type V2OrganizationStore interface {
	ListMemberships(context.Context, int) ([]tenancy.Membership, error)
	GetOrganization(context.Context, uuid.UUID) (tenancy.Organization, error)
}

type V2SystemHandler struct {
	organizations V2OrganizationStore
	// directProfileLogin reports whether /auth/profile-login is wired. Clients
	// follow capability discovery rather than version sniffing, so this must
	// track the actual route.
	directProfileLogin bool
	lifecyclePhase     func(context.Context) (lifecycleidempotency.Phase, error)
}

func NewV2SystemHandler(organizations V2OrganizationStore) *V2SystemHandler {
	return &V2SystemHandler{organizations: organizations}
}

// SetDirectProfileLoginAvailable records that direct profile login is served.
func (h *V2SystemHandler) SetDirectProfileLoginAvailable(available bool) {
	h.directProfileLogin = available
}

// SetLifecycleIdempotencyPhase records that the durable lifecycle coordinator
// is wired and supplies its current enforcement phase. Support and enforcement
// are separate tokens so clients can safely adopt keys during rollout.
func (h *V2SystemHandler) SetLifecycleIdempotencyPhase(reader func(context.Context) (lifecycleidempotency.Phase, error)) {
	h.lifecyclePhase = reader
}

// Aggregate feature tokens. Each names a client-visible capability of this
// build; the per-subsystem capability endpoints (/playback/capability,
// /events/capability, /auth/device/capability, …) stay the place to look for
// the details of one. Clients hold an allowlist of tokens they understand and
// ignore the rest, so adding one here is additive by construction.
const (
	// featureWatchDocumentV1 covers the versioned Watch documents served at
	// GET /api/v2/watch/home and GET /api/v2/watch/items/{content_id}.
	featureWatchDocumentV1 = "watch_document_v1"
	// featureDevicePairingV1 covers pairing a television against an account
	// without typing a password on it. Protocol details live on
	// /auth/device/capability.
	featureDevicePairingV1 = "device_pairing_v1"
	// featureProgressSyncV1 covers reading and writing playback progress,
	// including POST /api/v2/sync/progress and its updated/ignored/error
	// per-item results.
	featureProgressSyncV1 = "progress_sync_v1"
	// featureDeclaredEventChannels is the aggregate name for the events
	// websocket's declared-channel handshake, reported in detail as
	// declared_channels on /events/capability.
	featureDeclaredEventChannels = "declared_event_channels"
	featureMusicCatalogV1        = "music_catalog_v1"
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
	"music_album",
}

// v2CapabilityTokens is assembled once: the document describes the build, not
// the request or the caller. Playback contributes the same tokens it publishes
// on /playback/capability, taken from the function that owns them so the
// aggregate cannot drift away from the endpoint it summarizes.
var v2CapabilityTokens = buildV2CapabilityTokens()

func buildV2CapabilityTokens() []string {
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
	add(featureDeclaredEventChannels)
	add(featureWatchDocumentV1, featureDevicePairingV1, featureProgressSyncV1, featureMusicCatalogV1)
	return tokens
}

// v2CapabilitiesResponse is the public, unauthenticated answer to "what does
// this server do". Fields are additive-only: media_types and feature_tokens
// were added beside the original three without touching them, and the token
// list is where every later capability lands.
type v2CapabilitiesResponse struct {
	API            string               `json:"api"`
	IdentitySchema int                  `json:"identity_schema"`
	Features       v2CapabilityFeatures `json:"features"`
	// MediaTypes are the item types this build can serve.
	MediaTypes []string `json:"media_types"`
	// FeatureTokens is the token allowlist clients match against. It is a
	// separate field rather than an addition to Features because Features is a
	// fixed object of booleans on the wire already, and versioned tokens
	// (watch_document_v1) cannot be expressed as stable field names.
	FeatureTokens []string `json:"feature_tokens"`
}

type v2CapabilityFeatures struct {
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
func (h *V2SystemHandler) HandleCapabilities(w http.ResponseWriter, request *http.Request) {
	featureTokens := slices.Clone(v2CapabilityTokens)
	if h.lifecyclePhase != nil {
		featureTokens = append(featureTokens, "lifecycle_idempotency_v1")
		if phase, err := h.lifecyclePhase(request.Context()); err == nil && phase == lifecycleidempotency.PhaseRequired {
			featureTokens = append(featureTokens, "lifecycle_idempotency_required_v1")
		}
	}
	writeJSON(w, http.StatusOK, v2CapabilitiesResponse{
		API:            "v2",
		IdentitySchema: 1,
		Features: v2CapabilityFeatures{
			LegacySiloV1:            true,
			OrganizationMemberships: true,
			TenantBoundedMediaScope: true,
			DirectProfileLogin:      h.directProfileLogin,
		},
		MediaTypes:    slices.Clone(mediaTypesServed),
		FeatureTokens: featureTokens,
	})
}

type v2OrganizationListResponse struct {
	Organizations []v2Organization `json:"organizations"`
}

type v2Organization struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	Default          bool   `json:"default"`
	MembershipID     string `json:"membership_id"`
	MembershipRole   string `json:"membership_role"`
	PolicyRevision   int64  `json:"policy_revision"`
	SecurityRevision int64  `json:"security_revision"`
}

func (h *V2SystemHandler) HandleOrganizations(w http.ResponseWriter, r *http.Request) {
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

	result := make([]v2Organization, 0, len(memberships))
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
		result = append(result, v2Organization{
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

	writeJSON(w, http.StatusOK, v2OrganizationListResponse{Organizations: result})
}
