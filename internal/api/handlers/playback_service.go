package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

const initialPlaybackNotConfigured = "not_configured"

// PlaybackCaller carries authenticated identity and bounded client facts across
// adapters. InstallationID is supplied by the capability that admitted the client.
type PlaybackCaller struct {
	UserID                                                int
	ProfileID, InstallationID                             string
	DeviceID, DeviceName, Platform                        string
	UserAgent, RemoteAddr                                 string
	ClientName, ClientVersion, ClientBuild, ClientChannel string
}

type PlaybackCapabilitiesView struct {
	InstallationID, Revision, State string
	Allowed                         bool
	ProtocolVersions                []int
	Features                        []string
	Deliveries                      []playback.DeliveryV3
}

type PlaybackOperationError struct {
	Status        int
	Code, Message string
}

func (e *PlaybackOperationError) Error() string { return e.Message }
func playbackOperationError(status int, code, message string) *PlaybackOperationError {
	return &PlaybackOperationError{Status: status, Code: code, Message: message}
}
func playbackAuthorityOperationError() *PlaybackOperationError {
	return playbackOperationError(http.StatusServiceUnavailable, "unavailable", "Playback authority is temporarily unavailable")
}
func writePlaybackOperationError(w http.ResponseWriter, err error) {
	if e, ok := errors.AsType[*PlaybackOperationError](err); ok {
		writeError(w, e.Status, e.Code, e.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "Playback operation failed")
}
func playbackFileOperationError(err error) error {
	if errors.Is(err, catalog.ErrItemNotFound) || errors.Is(err, catalog.ErrEpisodeNotFound) {
		return playbackOperationError(http.StatusNotFound, "not_found", "Media file not found")
	}
	return playbackOperationError(http.StatusInternalServerError, "internal_error", "Failed to authorize media file")
}
func playbackPreflightOperationError(err error) error {
	if isPlaybackFileMissing(err) {
		return playbackOperationError(http.StatusNotFound, "not_found", "Source media file is missing")
	}
	return playbackOperationError(http.StatusInternalServerError, "internal_error", "Failed to access source media file")
}
func playbackPersistenceOperationError(err error) error {
	if errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
		return playbackOperationError(http.StatusConflict, "playback_attempt_reused", "The playback attempt ID belongs to a different request")
	}
	return playbackOperationError(http.StatusInternalServerError, "internal_error", "Failed to persist the playback decision")
}

func (h *PlaybackHandler) validatePlaybackCaller(ctx context.Context, caller PlaybackCaller) error {
	if caller.UserID <= 0 || caller.UserID != apimw.GetUserID(ctx) || caller.ProfileID == "" || caller.ProfileID != apimw.GetProfileID(ctx) {
		return playbackOperationError(http.StatusForbidden, "forbidden", "Playback identity does not match the authenticated profile")
	}
	if h.initialFlow == nil || h.initialFlow.InstallationID == "" {
		return playbackOperationError(http.StatusConflict, "capability_not_configured", "Initial playback is not configured")
	}
	if caller.InstallationID != h.initialFlow.InstallationID {
		return playbackOperationError(http.StatusConflict, "installation_changed", "Playback installation changed; refresh capabilities")
	}
	return nil
}

func (h *PlaybackHandler) PlaybackCapabilities(ctx context.Context, userID int, profileID string) (PlaybackCapabilitiesView, error) {
	view := PlaybackCapabilitiesView{State: initialPlaybackNotConfigured, ProtocolVersions: []int{playback.ProtocolV3}, Features: []string{}, Deliveries: []playback.DeliveryV3{}}
	if userID <= 0 || userID != apimw.GetUserID(ctx) || profileID == "" || profileID != apimw.GetProfileID(ctx) {
		return view, playbackOperationError(http.StatusForbidden, "forbidden", "Playback identity does not match the authenticated profile")
	}
	admission := ""
	if h.initialFlow != nil && h.initialFlow.InstallationID != "" {
		view.InstallationID = h.initialFlow.InstallationID
		source, err := h.initialFlow.Control.GetAdmittedPlaybackSource(ctx, userID)
		switch {
		case errors.Is(err, playback.ErrInitialActivationUnavailableV3):
			view.State = "not_admitted"
		case err != nil:
			return view, playbackAuthorityOperationError()
		default:
			view.State = "available"
			view.Allowed = true
			admission = source.AdmissionID
			view.Features = initialServerFeaturesV3()
			view.Deliveries = []playback.DeliveryV3{playback.DeliveryOriginalHTTPV3, playback.DeliveryTranscodeHLSV3}
		}
	}
	digest := sha256.Sum256([]byte(view.InstallationID + "|" + view.State + "|" + admission))
	view.Revision = hex.EncodeToString(digest[:])
	return view, nil
}

// The application pipeline still uses private request-based routing helpers.
// This request contains only caller facts; it is never dispatched to an HTTP
// handler and never carries credentials, a body stream, or a response writer.
func playbackCallerRequest(ctx context.Context, caller PlaybackCaller) *http.Request {
	headers := make(http.Header)
	headers.Set(deviceIDHeader, caller.DeviceID)
	headers.Set(deviceNameHeader, caller.DeviceName)
	headers.Set(devicePlatformHeader, caller.Platform)
	headers.Set("User-Agent", caller.UserAgent)
	headers.Set("X-Silo-Client", caller.ClientName)
	headers.Set("X-Silo-Client-Version", caller.ClientVersion)
	headers.Set("X-Silo-Client-Build", caller.ClientBuild)
	headers.Set("X-Silo-Client-Channel", caller.ClientChannel)
	return (&http.Request{Header: headers, RemoteAddr: caller.RemoteAddr, URL: &url.URL{}}).WithContext(ctx)
}
func (h *PlaybackHandler) StartInitialPlayback(ctx context.Context, caller PlaybackCaller, request playback.StartRequestV3) (playback.DecisionResponseV3, error) {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return playback.DecisionResponseV3{}, err
	}
	capability, err := h.PlaybackCapabilities(ctx, caller.UserID, caller.ProfileID)
	if err != nil {
		return playback.DecisionResponseV3{}, err
	}
	if !capability.Allowed {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "capability_disabled", "Playback source is not admitted")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid playback request")
	}
	return h.startPlaybackApplicationV3(playbackCallerRequest(ctx, caller), body)
}

func (h *PlaybackHandler) ApplyInitialProgress(ctx context.Context, caller PlaybackCaller, sessionID string, command PlaybackProgressCommand) (PlaybackMutationView, error) {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return PlaybackMutationView{}, err
	}
	if err := validatePlaybackSessionID(sessionID); err != nil {
		return PlaybackMutationView{}, err
	}
	if math.IsNaN(command.Position) || math.IsInf(command.Position, 0) {
		return PlaybackMutationView{}, playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid playback progress position")
	}
	return h.applyInitialProgress(ctx, caller.UserID, caller.ProfileID, sessionID, command)
}
func (h *PlaybackHandler) StopInitialPlayback(ctx context.Context, caller PlaybackCaller, sessionID string, command PlaybackStopCommand) (PlaybackMutationView, error) {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return PlaybackMutationView{}, err
	}
	if err := validatePlaybackSessionID(sessionID); err != nil {
		return PlaybackMutationView{}, err
	}
	if command.Position != nil && (math.IsNaN(*command.Position) || math.IsInf(*command.Position, 0)) {
		return PlaybackMutationView{}, playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid playback final position")
	}
	return h.stopInitialPlayback(ctx, caller.UserID, caller.ProfileID, sessionID, command)
}

func validatePlaybackSessionID(sessionID string) error {
	id, err := uuid.Parse(sessionID)
	if err != nil || id == uuid.Nil || id.String() != sessionID {
		return playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid playback session ID")
	}
	return nil
}

// Only advertise capabilities implemented by this initial direct/local-HLS flow.
// Replanning, route events and replacement are separate lifecycle operations.
func initialServerFeaturesV3() []string {
	return []string{playback.FeaturePlaybackPlanV3, playback.FeatureNeutralContractV3,
		playback.FeatureLayoutPassthrough, playback.FeatureDeviceQuirksV3,
		playback.FeatureOutputDisplayEvidenceV3, playback.FeatureDirectStreamResumeV3,
		playback.FeatureSoftwareVideoDecodeV3, playback.FeaturePlanSourceDurationV3,
		"sequenced_progress_v1"}
}
