package apiv2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/clientip"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

const playbackCacheControl = "no-store"

const (
	opReplanPlayback           = "replanPlayback"
	opReportPlaybackRouteEvent = "reportPlaybackRouteEvent"
	// playbackCodeCapabilityUnsupported is the service error code that maps to
	// the 501 capability_unsupported problem.
	playbackCodeCapabilityUnsupported = "capability_unsupported"
)

type PlaybackService interface {
	PlaybackCapabilities(context.Context, int, string) (handlers.PlaybackCapabilitiesView, error)
	StartInitialPlayback(context.Context, handlers.PlaybackCaller, playback.StartRequestV3) (playback.DecisionResponseV3, error)
	ApplyInitialProgress(context.Context, handlers.PlaybackCaller, string, handlers.PlaybackProgressCommand) (handlers.PlaybackMutationView, error)
	StopInitialPlayback(context.Context, handlers.PlaybackCaller, string, handlers.PlaybackStopCommand) (handlers.PlaybackMutationView, error)
	ReplanInitialPlayback(context.Context, handlers.PlaybackCaller, string, handlers.PlaybackReplanCommand) (playback.DecisionResponseV3, error)
	ReportInitialRouteEvent(context.Context, handlers.PlaybackCaller, handlers.PlaybackRouteEventCommand) error
}

type PlaybackCapabilities struct {
	InstallationID   ID                    `json:"installation_id,omitempty"`
	Revision         string                `json:"revision"`
	State            string                `json:"state"`
	Allowed          bool                  `json:"allowed"`
	ProtocolVersions []int                 `json:"protocol_versions"`
	Features         []string              `json:"features"`
	Deliveries       []playback.DeliveryV3 `json:"deliveries"`
}
type PlaybackStartBody struct {
	InstallationID             ID                                 `json:"installation_id" minLength:"1"`
	ProtocolVersion            int                                `json:"protocol_version"`
	ClientFeatures             []string                           `json:"client_features"`
	FileID                     ID                                 `json:"file_id"`
	ProfileID                  ID                                 `json:"profile_id"`
	PlaybackAttemptID          string                             `json:"playback_attempt_id"`
	QualityPreference          string                             `json:"quality_preference"`
	SubtitleFidelityPreference playback.SubtitleFidelityV3        `json:"subtitle_fidelity_preference"`
	StartPosition              *float64                           `json:"start_position,omitempty" nullable:"false"`
	ProgressPersistence        playback.ProgressPersistenceV3     `json:"progress_persistence,omitempty"`
	AudioTrackID               string                             `json:"audio_track_id,omitempty"`
	AudioTrackIndex            *int                               `json:"audio_track_index,omitempty" nullable:"false"`
	SubtitleTrackID            string                             `json:"subtitle_track_id,omitempty"`
	SubtitleTrackIndex         *int                               `json:"subtitle_track_index,omitempty" nullable:"false"`
	Metered                    bool                               `json:"metered"`
	BandwidthEstimateKbps      *int                               `json:"bandwidth_estimate_kbps,omitempty" nullable:"false"`
	BandwidthCapKbps           *int                               `json:"bandwidth_cap_kbps,omitempty" nullable:"false"`
	Capabilities               playback.ClientCodecCapabilitiesV3 `json:"client_capabilities"`
	ClientPlaybackContext      playback.ClientPlaybackContextV3   `json:"client_playback_context"`
}
type PlaybackPlan struct {
	ProtocolVersion        int                             `json:"protocol_version"`
	PlanID                 string                          `json:"plan_id"`
	PlanAttemptKey         string                          `json:"plan_attempt_key"`
	SessionID              string                          `json:"session_id,omitempty"`
	ExpiresAt              string                          `json:"expires_at,omitempty"`
	Delivery               playback.DeliveryV3             `json:"delivery"`
	Stream                 playback.StreamV3               `json:"stream"`
	Timeline               playback.TimelineV3             `json:"timeline"`
	SelectedTracks         playback.SelectedTracksV3       `json:"selected_tracks"`
	EffectiveRecipe        playback.EffectiveRecipeV3      `json:"effective_recipe"`
	Claims                 playback.ValidationClaimsV3     `json:"claims"`
	Subtitle               playback.SubtitleDecisionV3     `json:"subtitle"`
	Transformations        []playback.TransformationV3     `json:"transformations"`
	AppliedQuirks          []playback.AppliedQuirkV3       `json:"applied_quirks"`
	RuntimeCorrections     []string                        `json:"runtime_corrections"`
	AvailableQualities     []playback.AvailableQualityV3   `json:"available_qualities"`
	DegradationWarnings    []playback.DegradationWarningV3 `json:"degradation_warnings"`
	DecisionReason         string                          `json:"decision_reason"`
	RequestedMediaFileID   ID                              `json:"requested_media_file_id"`
	EffectiveMediaFileID   ID                              `json:"effective_media_file_id"`
	Source                 PlaybackSource                  `json:"source"`
	SubtitleFidelityPolicy string                          `json:"subtitle_fidelity_policy"`
}
type PlaybackSource struct {
	MediaFileID        ID                          `json:"media_file_id"`
	DurationSeconds    *float64                    `json:"duration_seconds,omitempty"`
	Container          string                      `json:"container,omitempty"`
	VideoCodec         string                      `json:"video_codec,omitempty"`
	VideoProfile       string                      `json:"video_profile,omitempty"`
	VideoLevel         int                         `json:"video_level,omitempty"`
	BitDepth           int                         `json:"bit_depth,omitempty"`
	ColorRange         string                      `json:"color_range,omitempty"`
	Width              int                         `json:"width,omitempty"`
	Height             int                         `json:"height,omitempty"`
	FrameRate          float64                     `json:"frame_rate,omitempty"`
	BitrateKbps        int                         `json:"bitrate_kbps,omitempty"`
	DynamicRange       string                      `json:"dynamic_range,omitempty"`
	HDR10Plus          bool                        `json:"hdr10_plus"`
	DVProfile          int                         `json:"dolby_vision_profile,omitempty"`
	DVLevel            int                         `json:"dolby_vision_level,omitempty"`
	DVBLCompatID       int                         `json:"dv_bl_compat_id,omitempty"`
	DVBaseLayerProven  bool                        `json:"dv_base_layer_proven,omitempty"`
	DVEnhancementLayer playback.EnhancementLayerV3 `json:"dv_enhancement_layer"`
	AudioCodec         string                      `json:"audio_codec,omitempty"`
	AudioChannels      int                         `json:"audio_channels,omitempty"`
	AudioLayout        string                      `json:"audio_layout,omitempty"`
	VideoCopyUnsafe    bool                        `json:"video_copy_unsafe,omitempty"`
}
type PlaybackDecision struct {
	ProtocolVersion int                        `json:"protocol_version"`
	ServerFeatures  []string                   `json:"server_features"`
	Outcome         playback.DecisionOutcomeV3 `json:"outcome"`
	SessionID       string                     `json:"session_id,omitempty"`
	PlaybackPlan    *PlaybackPlan              `json:"playback_plan,omitempty"`
	Terminal        *playback.TerminalV3       `json:"terminal,omitempty"`
}

type PlaybackRequestHeaders struct {
	UserAgent       string `header:"User-Agent"`
	DeviceID        string `header:"X-Device-ID"`
	ClientName      string `header:"X-Client-Name"`
	ClientVersion   string `header:"X-Client-Version"`
	ClientBuild     string `header:"X-Client-Build"`
	ClientChannel   string `header:"X-Client-Channel"`
	ClientModel     string `header:"X-Client-Model"`
	ClientPlatform  string `header:"X-Client-Platform"`
	ClientOSVersion string `header:"X-Client-OS-Version"`
}
type PlaybackStartInput struct {
	PlaybackRequestHeaders
	Body PlaybackStartBody
}
type PlaybackCapabilitiesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PlaybackCapabilities
}
type PlaybackStartOutput struct {
	Status int
	Body   PlaybackDecision
}
type PlaybackProgressBody struct {
	InstallationID ID      `json:"installation_id" minLength:"1"`
	Sequence       int64   `json:"sequence" minimum:"1"`
	Position       float64 `json:"position" minimum:"0"`
	IsPaused       bool    `json:"is_paused"`
}
type PlaybackStopBody struct {
	InstallationID ID       `json:"installation_id" minLength:"1"`
	StopID         ID       `json:"stop_id" minLength:"1"`
	Sequence       int64    `json:"sequence,omitempty" minimum:"1"`
	Position       *float64 `json:"position,omitempty" nullable:"false" minimum:"0"`
	IsPaused       bool     `json:"is_paused,omitempty"`
}
type PlaybackProgressInput struct {
	PlaybackRequestHeaders
	SessionID ID `path:"session_id" minLength:"1"`
	Body      PlaybackProgressBody
}
type PlaybackStopInput struct {
	PlaybackRequestHeaders
	SessionID ID `path:"session_id" minLength:"1"`
	Body      PlaybackStopBody
}
type PlaybackAccepted struct {
	Sequence int64   `json:"sequence"`
	Position float64 `json:"position"`
	IsPaused bool    `json:"is_paused"`
}
type PlaybackMutation struct {
	Outcome   string            `json:"outcome"`
	Accepted  *PlaybackAccepted `json:"accepted,omitempty"`
	StopID    ID                `json:"stop_id,omitempty"`
	HistoryID ID                `json:"history_id,omitempty"`
}
type PlaybackMutationOutput struct {
	Status int
	Body   PlaybackMutation
}

// PlaybackReplanBody is the v3 replan request under the installed authority.
// Only a position re-anchor of the current route is served by the initial flow;
// track, quality and output changes answer 501 capability_unsupported.
type PlaybackReplanBody struct {
	InstallationID        ID                                 `json:"installation_id" minLength:"1"`
	ProtocolVersion       int                                `json:"protocol_version"`
	ClientFeatures        []string                           `json:"client_features,omitempty"`
	Operation             playback.ReplanOperationV3         `json:"operation,omitempty" enum:"failure_recovery,seek_reanchor,seek_failure_recovery,track_change,quality_change,output_change"`
	PlaybackAttemptID     string                             `json:"playback_attempt_id" minLength:"8" maxLength:"128"`
	ReplanRequestID       string                             `json:"replan_request_id" minLength:"8" maxLength:"128" doc:"Client-minted identity of this replan; a retry with the same body replays the durable decision"`
	FailedPlanID          string                             `json:"failed_plan_id" minLength:"8" maxLength:"128"`
	PlanAttemptID         string                             `json:"plan_attempt_id" minLength:"8" maxLength:"128"`
	PlanAttemptKey        string                             `json:"plan_attempt_key" minLength:"8" maxLength:"128"`
	AttemptedPlanKeys     []string                           `json:"attempted_plan_keys" maxItems:"16"`
	LocalMutations        []string                           `json:"local_mutations,omitempty" maxItems:"8"`
	AttemptCount          int                                `json:"attempt_count" minimum:"1" maximum:"8"`
	QualityPreference     string                             `json:"quality_preference"`
	PositionSeconds       float64                            `json:"position_seconds" minimum:"0"`
	Metered               bool                               `json:"metered"`
	BandwidthEstimateKbps *int                               `json:"bandwidth_estimate_kbps,omitempty" nullable:"false"`
	BandwidthCapKbps      *int                               `json:"bandwidth_cap_kbps,omitempty" nullable:"false"`
	SelectedTracks        playback.SelectedTracksV3          `json:"selected_tracks"`
	Failure               playback.FailureV3                 `json:"failure,omitzero"`
	Capabilities          playback.ClientCodecCapabilitiesV3 `json:"client_capabilities"`
	ClientPlaybackContext playback.ClientPlaybackContextV3   `json:"client_playback_context"`
}
type PlaybackReplanInput struct {
	PlaybackRequestHeaders
	SessionID ID `path:"session_id" minLength:"1"`
	Body      PlaybackReplanBody
}
type PlaybackReplanOutput struct {
	Body PlaybackDecision
}

// PlaybackRouteEventBody is the v3 diagnostic route event plus a client-minted
// event identity. Diagnostics never control playback and a 429 means drop.
type PlaybackRouteEventBody struct {
	InstallationID        ID                `json:"installation_id" minLength:"1"`
	EventID               ID                `json:"event_id" minLength:"1" doc:"Client-minted UUID; a retry with the same id after a lost 202 is recorded once"`
	ProtocolVersion       int               `json:"protocol_version"`
	PlaybackAttemptID     string            `json:"playback_attempt_id" minLength:"8" maxLength:"128"`
	SessionID             string            `json:"session_id,omitempty" maxLength:"128"`
	PlanID                string            `json:"plan_id,omitempty" maxLength:"128"`
	PlanAttemptID         string            `json:"plan_attempt_id,omitempty" maxLength:"128"`
	PlanAttemptKey        string            `json:"plan_attempt_key,omitempty" maxLength:"128"`
	Event                 string            `json:"event" enum:"plan_selected,plan_invalidated,plan_failed,first_frame,terminal,stopped,runtime_correction_applied,runtime_correction_succeeded,runtime_correction_failed,seek_reanchor_requested,seek_reanchored"`
	FailureClassification string            `json:"failure_classification,omitempty" maxLength:"64"`
	FallbackReason        string            `json:"fallback_reason,omitempty" maxLength:"64"`
	AppliedQuirkIDs       []string          `json:"applied_quirk_ids,omitempty" maxItems:"16"`
	QuirkRegistryRevision string            `json:"quirk_registry_revision,omitempty" maxLength:"128"`
	OutputContextID       string            `json:"output_context_id,omitempty" maxLength:"128"`
	Diagnostics           map[string]string `json:"diagnostics" maxProperties:"32"`
}
type PlaybackRouteEventInput struct {
	PlaybackRequestHeaders
	Body PlaybackRouteEventBody
}

// PlaybackRouteEventReceipt acknowledges that the identified event was queued
// for recording. It is not proof of a durable write; a lost reply may be
// retried with the same event_id and is recorded once.
type PlaybackRouteEventReceipt struct {
	EventID ID     `json:"event_id"`
	Outcome string `json:"outcome" enum:"accepted"`
}
type PlaybackRouteEventOutput struct {
	Status int
	Body   PlaybackRouteEventReceipt
}

func registerPlayback(reg *Registry) {
	op := func(method, path, id string) Operation {
		operation := Operation{Operation: humaOp(method, Prefix+"/playback"+path, id, "playback", "Use the installed playback authority and exact selected progress source."), Class: ClassProfileScoped, ServiceBacked: true}
		if method != http.MethodGet {
			operation.RetrySafety = RetrySafetyDomainIdentity
		}

		if id == "startPlayback" {
			operation.DefaultStatus = http.StatusCreated
		}
		if id == "stopPlayback" {
			operation.Responses = map[string]*huma.Response{"202": {Description: "The terminal receipt is committed; retry the same stop ID after outstanding grants drain.", Content: map[string]*huma.MediaType{mediaTypeJSON: {Schema: reg.api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[PlaybackMutation](), true, "")}}}}
		}
		operation.Errors = []int{http.StatusConflict, http.StatusUnprocessableEntity, http.StatusServiceUnavailable}
		if id == opReplanPlayback {
			operation.Summary = "Re-anchor the current initial route at a new position under the installed authority. Track, quality and output changes are not served by the initial flow."
			operation.Errors = append(operation.Errors, http.StatusNotFound, http.StatusNotImplemented)
		}
		if id == opReportPlaybackRouteEvent {
			operation.Summary = "Record one playback route diagnostic for an attempt this profile owns. Never retried automatically; a 429 means drop the event."
			operation.RetrySafety = RetrySafetyNonRetryable
			operation.DefaultStatus = http.StatusAccepted
			operation.Errors = []int{http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable}
		}
		return operation
	}
	Register(reg, op(http.MethodGet, "/capabilities", "getPlaybackCapabilities"), func(ctx context.Context, _ *struct{}) (*PlaybackCapabilitiesOutput, error) {
		userID, profileID, p := viewerIdentity(ctx)
		if p != nil {
			return nil, p
		}
		if reg.deps.Playback == nil {
			return &PlaybackCapabilitiesOutput{CacheControl: playbackCacheControl, Body: PlaybackCapabilities{Revision: "not-configured", State: StateNotConfigured, ProtocolVersions: []int{}, Features: []string{}, Deliveries: []playback.DeliveryV3{}}}, nil
		}
		view, err := reg.deps.Playback.PlaybackCapabilities(ctx, userID, profileID)
		if err != nil {
			return nil, playbackProblem(err)
		}
		return &PlaybackCapabilitiesOutput{CacheControl: playbackCacheControl, Body: PlaybackCapabilities{InstallationID: ID(view.InstallationID), Revision: view.Revision, State: view.State, Allowed: view.Allowed, ProtocolVersions: append([]int{}, view.ProtocolVersions...), Features: append([]string{}, view.Features...), Deliveries: append([]playback.DeliveryV3{}, view.Deliveries...)}}, nil
	})
	Register(reg, op(http.MethodPost, "/start", "startPlayback"), func(ctx context.Context, in *PlaybackStartInput) (*PlaybackStartOutput, error) {
		caller, p := reg.playbackCaller(ctx, in.PlaybackRequestHeaders, in.Body.InstallationID)
		if p != nil {
			return nil, p
		}
		fileID, p := in.Body.FileID.positive("body.file_id")
		if p != nil {
			return nil, p
		}
		if string(in.Body.ProfileID) != caller.ProfileID {
			return nil, validationProblem("body.profile_id", "invalid", "Profile must match the authenticated viewer.")
		}
		request := in.Body.domain(fileID)
		validationData, err := json.Marshal(request)
		if err != nil {
			return nil, validationProblem("body", "invalid", "Invalid playback request.")
		}
		var validationRequest playback.StartRequestV3
		if err := json.Unmarshal(validationData, &validationRequest); err != nil {
			return nil, validationProblem("body", "invalid", "Invalid playback request.")
		}
		if _, err := validationRequest.NormalizeAndValidate(); err != nil {
			return nil, validationProblem("body", "invalid", err.Error())
		}
		response, err := reg.deps.Playback.StartInitialPlayback(ctx, caller, request)
		if err != nil {
			return nil, playbackProblem(err)
		}
		return &PlaybackStartOutput{Status: http.StatusCreated, Body: playbackDecision(response)}, nil
	})
	Register(reg, op(http.MethodPost, "/{session_id}/progress", "updatePlaybackProgress"), func(ctx context.Context, in *PlaybackProgressInput) (*PlaybackMutationOutput, error) {
		caller, p := reg.playbackCaller(ctx, in.PlaybackRequestHeaders, in.Body.InstallationID)
		if p != nil {
			return nil, p
		}
		view, err := reg.deps.Playback.ApplyInitialProgress(ctx, caller, string(in.SessionID), handlers.PlaybackProgressCommand{Sequence: in.Body.Sequence, Position: in.Body.Position, IsPaused: in.Body.IsPaused})
		return playbackMutation(view, err)
	})
	registerPlaybackReplan(reg, op)
	registerPlaybackRouteEvents(reg, op)
	Register(reg, op(http.MethodDelete, "/{session_id}", "stopPlayback"), func(ctx context.Context, in *PlaybackStopInput) (*PlaybackMutationOutput, error) {
		caller, p := reg.playbackCaller(ctx, in.PlaybackRequestHeaders, in.Body.InstallationID)
		if p != nil {
			return nil, p
		}
		if !playbackUUID(string(in.Body.StopID)) {
			return nil, validationProblem("body.stop_id", "invalid", "Expected a canonical UUID.")
		}
		if (in.Body.Sequence == 0) != (in.Body.Position == nil) {
			return nil, validationProblem("body", "invalid", "A final sample requires sequence and position together.")
		}
		view, err := reg.deps.Playback.StopInitialPlayback(ctx, caller, string(in.SessionID), handlers.PlaybackStopCommand{StopID: string(in.Body.StopID), Sequence: in.Body.Sequence, Position: in.Body.Position, IsPaused: in.Body.IsPaused})
		return playbackMutation(view, err)
	})
}
func registerPlaybackReplan(reg *Registry, op func(method, path, id string) Operation) {
	Register(reg, op(http.MethodPost, "/{session_id}/replan", opReplanPlayback), func(ctx context.Context, in *PlaybackReplanInput) (*PlaybackReplanOutput, error) {
		caller, p := reg.playbackCaller(ctx, in.PlaybackRequestHeaders, in.Body.InstallationID)
		if p != nil {
			return nil, p
		}
		request := in.Body.domain()
		if err := request.Validate(); err != nil {
			return nil, validationProblem("body", "invalid", err.Error())
		}
		// The digest fingerprints the exact accepted body, not the raw bytes,
		// so header-only differences never masquerade as a reused request id.
		canonical, err := json.Marshal(request)
		if err != nil {
			return nil, validationProblem("body", "invalid", "Invalid replan request.")
		}
		response, err := reg.deps.Playback.ReplanInitialPlayback(ctx, caller, string(in.SessionID), handlers.PlaybackReplanCommand{Request: request, Digest: handlers.ReplanDigestV3(canonical)})
		if err != nil {
			return nil, playbackProblem(err)
		}
		return &PlaybackReplanOutput{Body: playbackDecision(response)}, nil
	})
}
func (in PlaybackReplanBody) domain() playback.ReplanRequestV3 {
	return playback.ReplanRequestV3{ProtocolVersion: in.ProtocolVersion, ClientFeatures: in.ClientFeatures, Operation: in.Operation, PlaybackAttemptID: in.PlaybackAttemptID, ReplanRequestID: in.ReplanRequestID, FailedPlanID: in.FailedPlanID, PlanAttemptID: in.PlanAttemptID, PlanAttemptKey: in.PlanAttemptKey, AttemptedPlanKeys: in.AttemptedPlanKeys, LocalMutations: in.LocalMutations, AttemptCount: in.AttemptCount, QualityPreference: in.QualityPreference, PositionSeconds: in.PositionSeconds, Metered: in.Metered, BandwidthEstimateKbps: in.BandwidthEstimateKbps, BandwidthCapKbps: in.BandwidthCapKbps, SelectedTracks: in.SelectedTracks, Failure: in.Failure, Capabilities: in.Capabilities, ClientPlaybackContext: in.ClientPlaybackContext}
}
func registerPlaybackRouteEvents(reg *Registry, op func(method, path, id string) Operation) {
	Register(reg, op(http.MethodPost, "/route-events", opReportPlaybackRouteEvent), func(ctx context.Context, in *PlaybackRouteEventInput) (*PlaybackRouteEventOutput, error) {
		caller, p := reg.playbackCaller(ctx, in.PlaybackRequestHeaders, in.Body.InstallationID)
		if p != nil {
			return nil, p
		}
		if !playbackUUID(string(in.Body.EventID)) {
			return nil, validationProblem("body.event_id", "invalid", "Expected a canonical UUID.")
		}
		b := in.Body
		event := playback.RouteEventV3{ProtocolVersion: b.ProtocolVersion, PlaybackAttemptID: b.PlaybackAttemptID, SessionID: b.SessionID, PlanID: b.PlanID, PlanAttemptID: b.PlanAttemptID, PlanAttemptKey: b.PlanAttemptKey, Event: b.Event, FailureClassification: b.FailureClassification, FallbackReason: b.FallbackReason, AppliedQuirkIDs: b.AppliedQuirkIDs, QuirkRegistryRevision: b.QuirkRegistryRevision, OutputContextID: b.OutputContextID, Diagnostics: b.Diagnostics}
		if err := reg.deps.Playback.ReportInitialRouteEvent(ctx, caller, handlers.PlaybackRouteEventCommand{EventID: string(b.EventID), Event: event}); err != nil {
			return nil, playbackProblem(err)
		}
		return &PlaybackRouteEventOutput{Status: http.StatusAccepted, Body: PlaybackRouteEventReceipt{EventID: b.EventID, Outcome: adminHistoryAccepted}}, nil
	})
}
func playbackUUID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}
func (reg *Registry) playbackCaller(ctx context.Context, headers PlaybackRequestHeaders, installation ID) (handlers.PlaybackCaller, *Problem) {
	if reg.deps.Playback == nil {
		return handlers.PlaybackCaller{}, NewProblem(TypeCapabilityNotConfigured, "Playback is not configured.")
	}
	userID, profileID, p := viewerIdentity(ctx)
	if p != nil {
		return handlers.PlaybackCaller{}, p
	}
	if !playbackUUID(string(installation)) {
		return handlers.PlaybackCaller{}, validationProblem("body.installation_id", "invalid", "Expected the installation identifier from capabilities.")
	}
	return handlers.PlaybackCaller{UserID: userID, ProfileID: profileID, InstallationID: string(installation), DeviceID: headers.DeviceID, UserAgent: headers.UserAgent, ClientName: headers.ClientName, ClientVersion: headers.ClientVersion, ClientBuild: headers.ClientBuild, ClientChannel: headers.ClientChannel, DeviceName: headers.ClientModel, Platform: headers.ClientPlatform, RemoteAddr: clientip.FromContext(ctx)}, nil
}
func playbackProblem(err error) *Problem {
	if operation, ok := errors.AsType[*handlers.PlaybackOperationError](err); ok {
		status := operation.Status
		if status == http.StatusBadRequest || status == http.StatusUpgradeRequired {
			status = http.StatusUnprocessableEntity
		}
		kind := TypeForStatus(status)
		if operation.Code == "playback_attempt_reused" {
			kind = TypeIdempotencyConflict
		}
		if operation.Code == "progress_conflict" {
			kind = TypeConflict
		}
		if operation.Code == "event_rate_limited" {
			kind = TypeRateLimited
		}
		if operation.Code == playbackCodeCapabilityUnsupported {
			kind = TypeCapabilityUnsupported
		}
		if operation.Code == "session_not_found" {
			kind = TypeNotFound
		}
		for _, candidate := range Catalog() {
			if candidate.ID == operation.Code && candidate.Status == status {
				kind = candidate
				break
			}
		}
		return NewProblem(kind, operation.Message)
	}
	return NewProblem(TypeDependencyUnavailable, "Playback is temporarily unavailable.")
}
func playbackMutation(view handlers.PlaybackMutationView, err error) (*PlaybackMutationOutput, error) {
	if err != nil {
		return nil, playbackProblem(err)
	}
	body := PlaybackMutation{Outcome: view.Outcome, StopID: ID(view.StopID), HistoryID: ID(view.HistoryID)}
	if view.Accepted != nil {
		body.Accepted = &PlaybackAccepted{Sequence: view.Accepted.Sequence, Position: view.Accepted.Position, IsPaused: view.Accepted.IsPaused}
	}
	status := http.StatusOK
	if view.Draining {
		status = http.StatusAccepted
	}
	return &PlaybackMutationOutput{Status: status, Body: body}, nil
}
func (in PlaybackStartBody) domain(fileID int) playback.StartRequestV3 {
	return playback.StartRequestV3{ProtocolVersion: in.ProtocolVersion, ClientFeatures: in.ClientFeatures, FileID: fileID, ProfileID: string(in.ProfileID), PlaybackAttemptID: in.PlaybackAttemptID, QualityPreference: in.QualityPreference, SubtitleFidelityPreference: in.SubtitleFidelityPreference, StartPosition: in.StartPosition, ProgressPersistence: in.ProgressPersistence, AudioTrackID: in.AudioTrackID, AudioTrackIndex: in.AudioTrackIndex, SubtitleTrackID: in.SubtitleTrackID, SubtitleTrackIndex: in.SubtitleTrackIndex, Metered: in.Metered, BandwidthEstimateKbps: in.BandwidthEstimateKbps, BandwidthCapKbps: in.BandwidthCapKbps, Capabilities: in.Capabilities, ClientPlaybackContext: in.ClientPlaybackContext}
}
func playbackDecision(in playback.DecisionResponseV3) PlaybackDecision {
	out := PlaybackDecision{ProtocolVersion: in.ProtocolVersion, ServerFeatures: in.ServerFeatures, Outcome: in.Outcome, SessionID: in.SessionID, Terminal: in.Terminal}
	if in.PlaybackPlan != nil {
		p := in.PlaybackPlan
		stream := p.Stream
		stream.URL = playbackV2MediaURL(stream.URL)
		out.PlaybackPlan = &PlaybackPlan{ProtocolVersion: p.ProtocolVersion, PlanID: p.PlanID, PlanAttemptKey: p.PlanAttemptKey, SessionID: p.SessionID, ExpiresAt: p.ExpiresAt, Delivery: p.Delivery, Stream: stream, Timeline: p.Timeline, SelectedTracks: p.SelectedTracks, EffectiveRecipe: p.EffectiveRecipe, Claims: p.Claims, Subtitle: playbackV2Subtitle(p.Subtitle), Transformations: p.Transformations, AppliedQuirks: p.AppliedQuirks, RuntimeCorrections: p.RuntimeCorrections, AvailableQualities: p.AvailableQualities, DegradationWarnings: p.DegradationWarnings, DecisionReason: p.DecisionReason, RequestedMediaFileID: ID(strconv.Itoa(p.RequestedMediaFileID)), EffectiveMediaFileID: ID(strconv.Itoa(p.EffectiveMediaFileID)), Source: playbackSource(p.Source), SubtitleFidelityPolicy: p.SubtitleFidelityPolicy}
	}
	return out
}
func playbackSource(in playback.SourceDescriptorV3) PlaybackSource {
	return PlaybackSource{MediaFileID: ID(strconv.Itoa(in.MediaFileID)), DurationSeconds: in.DurationSeconds, Container: in.Container, VideoCodec: in.VideoCodec, VideoProfile: in.VideoProfile, VideoLevel: in.VideoLevel, BitDepth: in.BitDepth, ColorRange: in.ColorRange, Width: in.Width, Height: in.Height, FrameRate: in.FrameRate, BitrateKbps: in.BitrateKbps, DynamicRange: in.DynamicRange, HDR10Plus: in.HDR10Plus, DVProfile: in.DVProfile, DVLevel: in.DVLevel, DVBLCompatID: in.DVBLCompatID, DVBaseLayerProven: in.DVBaseLayerProven, DVEnhancementLayer: in.DVEnhancementLayer, AudioCodec: in.AudioCodec, AudioChannels: in.AudioChannels, AudioLayout: in.AudioLayout, VideoCopyUnsafe: in.VideoCopyUnsafe}
}

// playbackV2MediaURL projects an API-local v1 media, sidecar or font URL into
// the v2 namespace without touching its signed query. Any other URL (absolute,
// proxy, or a legacy session-relative sidecar) is returned unchanged.
func playbackV2MediaURL(raw string) string {
	if strings.HasPrefix(raw, "/api/v1/stream/") || strings.HasPrefix(raw, "/api/v1/playback/transcode/") {
		return Prefix + strings.TrimPrefix(raw, "/api/v1")
	}
	return raw
}

// playbackV2Subtitle copies the subtitle decision so the v2 projection never
// mutates the persisted decision, then projects every sidecar and font URL.
func playbackV2Subtitle(in playback.SubtitleDecisionV3) playback.SubtitleDecisionV3 {
	out := in
	out.Inventory = append([]playback.SubtitleInventoryItemV3(nil), in.Inventory...)
	for i := range out.Inventory {
		out.Inventory[i].URL = playbackV2MediaURL(out.Inventory[i].URL)
		out.Inventory[i].FontBundleURL = playbackV2MediaURL(out.Inventory[i].FontBundleURL)
	}
	if in.Artifact != nil {
		artifact := *in.Artifact
		artifact.URL = playbackV2MediaURL(artifact.URL)
		out.Artifact = &artifact
	}
	return out
}
