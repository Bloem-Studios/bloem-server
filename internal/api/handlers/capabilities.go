package handlers

import (
	"net/http"
	"slices"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/downloads"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// capabilitySchemaVersion is the version of the aggregate capability document
// itself, not of any feature in it. It changes only if the three-field shape
// below changes, which the v1 rules make an additive-only event.
const capabilitySchemaVersion = 1

// Aggregate feature tokens. Each names a client-visible capability of this
// server; the per-subsystem capability endpoints (/playback/capability,
// /events/capability, /auth/device/capability, …) stay the place to look for
// the details of one. Clients hold an allowlist of tokens they understand and
// ignore the rest, so adding one here is additive by construction.
const (
	// featureWatchDocumentV1 covers the versioned Watch documents.
	featureWatchDocumentV1 = "watch_document_v1"
	// featureDevicePairingV1 covers pairing a television against an account
	// without typing a password on it. Protocol details live on
	// /auth/device/capability.
	featureDevicePairingV1 = "device_pairing_v1"
	// featureProgressSyncV1 covers reading and writing playback progress
	// through /progress and /sync/progress.
	featureProgressSyncV1 = "progress_sync_v1"
	// featureDeclaredEventChannels is the aggregate name for the events
	// websocket's declared-channel handshake, reported in detail as
	// declared_channels on /events/capability.
	featureDeclaredEventChannels = "declared_event_channels"
	// featureOfflineManifestPrefix names the managed offline manifest by the
	// version GET /downloads/{id}/manifest actually emits. Composed from
	// downloads.ManifestVersion rather than written out, so bumping the
	// manifest DTO cannot leave a stale token advertised here.
	featureOfflineManifestPrefix = "offline_manifest_v"
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

// capabilitySet is the aggregate capability document served by
// GET /api/v1/capabilities. The shape is the client contract's CapabilitySet.
type capabilitySet struct {
	SchemaVersion int      `json:"schema_version"`
	MediaTypes    []string `json:"media_types"`
	Features      []string `json:"features"`
}

// CapabilitiesHandler serves the public aggregate capability document. It is
// the one place a client can ask what this server does before authenticating,
// so that optional features are feature-detected rather than inferred from a
// server version.
type CapabilitiesHandler struct {
	mediaTypes []string
	features   []string
}

// NewCapabilitiesHandler assembles the advertised capability set once. The
// document is a property of the build, not of the request or the caller.
func NewCapabilitiesHandler() *CapabilitiesHandler {
	// Playback advertises the same tokens here as on /playback/capability;
	// taking them from the one function that owns them keeps the aggregate from
	// drifting away from the endpoint it summarizes.
	playbackFeatures := playback.ServerFeaturesV3()

	features := make([]string, 0, len(playbackFeatures)+4)
	add := func(tokens ...string) {
		for _, token := range tokens {
			if !slices.Contains(features, token) {
				features = append(features, token)
			}
		}
	}
	add(playbackFeatures...)
	add(featureDeclaredEventChannels)
	add(featureOfflineManifestPrefix + strconv.Itoa(downloads.ManifestVersion))
	add(featureWatchDocumentV1, featureDevicePairingV1, featureProgressSyncV1)

	return &CapabilitiesHandler{
		mediaTypes: slices.Clone(mediaTypesServed),
		features:   features,
	}
}

// HandleGetCapabilities answers the public capability probe. It is
// unauthenticated and never fails: a probe that can itself be unavailable
// leaves a client interpreting the same ambiguous status the probe exists to
// replace.
func (h *CapabilitiesHandler) HandleGetCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.capabilities())
}

// capabilities returns a fresh document. The slices are cloned so no caller —
// including a future one that post-processes the set — can mutate the shared
// advertisement.
func (h *CapabilitiesHandler) capabilities() capabilitySet {
	return capabilitySet{
		SchemaVersion: capabilitySchemaVersion,
		MediaTypes:    slices.Clone(h.mediaTypes),
		Features:      slices.Clone(h.features),
	}
}
