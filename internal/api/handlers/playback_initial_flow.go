package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/noderouting"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type InitialPlaybackControlV3 interface {
	playback.GrantPlanStoreV3
	playback.InitialActivationStoreV3
	playback.BoundPlaybackControlStoreV3
	playback.ExecutorRecipePlanStoreV3
	CancelInitialActivation(context.Context, playback.InitialActivationBindingV3, string) (playback.InitialActivationV3, error)
	RenewAttemptLease(context.Context, playback.AttemptAuthorityV3, time.Duration) (playback.AttemptLeaseV3, error)
}

type InitialPlaybackRecipesV3 interface {
	PutImmutable(context.Context, playback.RecipeCard) (playback.ExecutorRecipeLocatorV3, error)
}

// InitialPlaybackFlowV3 is explicitly configured for enrolled sources. Ordinary
// starts never create source markers or admit an account as a side effect.
type InitialPlaybackFlowV3 struct {
	Control       InitialPlaybackControlV3
	Sources       userstore.PlaybackSourceProvider
	Recipes       InitialPlaybackRecipesV3
	OwnerID       string
	Context       context.Context
	Clock         playback.RuntimeGrantClockV3
	Policy        playback.RuntimeGrantPolicyV3
	AcquireGrant  func(context.Context, string, playback.ExecutorNamespaceV3, playback.AttemptGrantPurposeV3) (*playback.RuntimeGrantV3, error)
	ResolveRecipe func(context.Context, string, playback.ExecutorNamespaceV3) (*playback.RecipeCard, error)
	pending       sync.Map
	owners        sync.Map
}

func (h *PlaybackHandler) ConfigureInitialPlaybackV3(flow *InitialPlaybackFlowV3) error {
	if flow == nil || flow.Control == nil || flow.Sources == nil || flow.Recipes == nil || flow.Context == nil || flow.Clock == nil || flow.AcquireGrant == nil || flow.ResolveRecipe == nil {
		return errors.New("initial playback dependencies required")
	}
	id, err := uuid.Parse(flow.OwnerID)
	if err != nil || id == uuid.Nil || id.String() != flow.OwnerID {
		return errors.New("initial playback boot owner required")
	}
	if err := flow.Policy.Validate(); err != nil {
		return err
	}
	if _, ok := h.sessionMgr.(initialSessionManagerV3); !ok {
		return errors.New("staged session manager required")
	}
	h.initialFlow = flow
	h.PlanStoreV3 = flow.Control
	h.tm.ExecuteGrants = flow.AcquireGrant
	h.tm.ResolveExecutorRecipe = flow.ResolveRecipe
	return nil
}

type initialSessionManagerV3 interface {
	StageInitialSession(context.Context, playback.InitialActivationBindingV3, int, int, playback.PlayMethod, bool) (*playback.Session, error)
	PublishInitialSession(context.Context, playback.InitialActivationBindingV3, playback.Session) (*playback.Session, error)
	DiscardInitialSession(context.Context, playback.InitialActivationBindingV3) error
}

func (h *PlaybackHandler) startInitialPlaybackV3(r *http.Request, userID int, profileID string, req playback.StartRequestV3, digests playbackStartRequestDigestsV3, requested, effective *models.MediaFile, audioIndex int, result playback.PlannerResultV3, clientInfo playback.ClientInfo) (playback.DecisionResponseV3, *transportErrorV3) {
	fail := func(err error) (playback.DecisionResponseV3, *transportErrorV3) {
		return playback.DecisionResponseV3{}, &transportErrorV3{reason: policyErrorUnavailable, message: "Playback authority is temporarily unavailable.", retryable: true, cause: err}
	}
	flow := h.initialFlow
	isTranscode := result.Plan != nil && result.PlayMethod == playback.PlayTranscode && result.Plan.Delivery == playback.DeliveryTranscodeHLSV3
	if result.Plan == nil || (!isTranscode && (result.PlayMethod != playback.PlayDirect || result.Plan.Delivery != playback.DeliveryOriginalHTTPV3)) {
		return fail(errors.New("initial flow requires supported direct or local encoded HLS"))
	}
	if h.JWTSecret == "" {
		return fail(errors.New("signed executor reference is required"))
	}
	source, err := flow.Control.GetAdmittedPlaybackSource(r.Context(), userID)
	if err != nil {
		return fail(err)
	}
	reservation, err := flow.Control.ReserveAttempt(r.Context(), playback.AttemptReservationRequestV3{PlaybackAttemptID: req.PlaybackAttemptID, UserID: userID, ProfileID: profileID, RequestedMediaFileID: requested.ID, RequestDigest: digests.current, NormalizedRequest: req, OwnerID: flow.OwnerID, LeaseDuration: flow.Policy.MaxDuration, Retention: playback.MaxTokenTTL})
	if err != nil {
		return fail(err)
	}
	if !reservation.Owned {
		if reservation.Record != nil {
			if response, err := h.recoverInitialPublicationV3(r.Context(), reservation.Record); err == nil {
				return response, nil
			}
		}
		return fail(errors.New("initial playback is already reserved"))
	}
	target := playbackProgressTarget(requested)
	if target == "" {
		return fail(errors.New("playback progress target missing"))
	}
	identity := userstore.WatchIdentity{}
	if h.StableIdentityResolver != nil {
		identity = h.StableIdentityResolver.ResolveHistoryIdentity(r.Context(), target)
	}
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return fail(err)
	}
	binding := playback.InitialActivationBindingV3{Source: source.Source, AdmissionID: source.AdmissionID, IntentID: uuid.NewString(), Scope: userstore.PlaybackProgressScope{ProfileID: profileID, SessionID: uuid.NewString(), MediaItemID: target}, Fence: userstore.PlaybackProgressFence{AttemptID: req.PlaybackAttemptID, Incarnation: reservation.Authority.Incarnation, OwnerID: reservation.Authority.OwnerID, Epoch: reservation.Authority.Epoch}, Progress: userstore.PlaybackProgressSample{DurationSeconds: float64(requested.Duration), PersistenceDisabled: req.ProgressPersistence == playback.ProgressPersistenceClientV3 || !sessionOwnsResumeTimelineV3(effective), Thresholds: h.playbackThresholds(r.Context()), Hints: userstore.VersionHints{FileID: requested.ID, Resolution: requested.Resolution, HDR: requested.HDR, CodecVideo: requested.CodecVideo, EditionKey: requested.EditionKey}}, HistoryIdentityJSON: string(identityJSON)}
	owner, err := playback.AcquireRuntimeOwnerLeaseV3(flow.Context, flow.Control.RenewAttemptLease, flow.Clock, flow.Policy, reservation.Authority)
	if err != nil {
		return fail(err)
	}
	startupCtx, cancelStartup := context.WithCancel(r.Context())
	defer cancelStartup()
	stopOwnerCancellation := context.AfterFunc(owner.Context(), cancelStartup)
	defer stopOwnerCancellation()
	r = r.WithContext(startupCtx)
	retained := false
	defer func() {
		if !retained {
			owner.Close()
		}
	}()
	manager, ok := h.sessionMgr.(initialSessionManagerV3)
	if !ok {
		return fail(errors.New("staged session manager unavailable"))
	}
	stage, err := manager.StageInitialSession(playback.WithClientInfo(r.Context(), clientInfo), binding, effective.ID, requested.ID, result.PlayMethod, result.TranscodeAudio)
	if err != nil {
		return fail(err)
	}
	published := false
	retainStage := false
	defer func() {
		if !published && !retainStage {
			_ = manager.DiscardInitialSession(context.WithoutCancel(r.Context()), binding)
		}
	}()
	if _, err = flow.Control.BeginInitialActivation(r.Context(), binding); err != nil {
		return fail(err)
	}
	abortID := uuid.NewString()
	abort := func(cause error) (playback.DecisionResponseV3, *transportErrorV3) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		state, cancelErr := flow.Control.CancelInitialActivation(cleanup, binding, abortID)
		if cancelErr == nil && state.Phase == playback.InitialActivationAbortingV3 {
			_ = h.reconcileInitialAbortV3(cleanup, binding, abortID)
		}
		return fail(cause)
	}
	sink, err := flow.Sources.OpenPlaybackSink(r.Context(), binding.Source)
	if err != nil {
		return abort(err)
	}
	defer sink.Close() //nolint:errcheck
	if err = owner.Check(); err != nil {
		return abort(err)
	}
	if _, err = sink.InstallPlaybackAuthority(r.Context(), userstore.InstallPlaybackAuthorityRequest{Scope: binding.Scope, Next: binding.Fence}); err != nil {
		// A failed reply may follow commit. Read the exact source before deciding.
		if _, readErr := playback.ReadInitialActivationReceiptV3(r.Context(), binding, sink); readErr != nil {
			return abort(err)
		}
	}
	observed, err := playback.ReadInitialActivationReceiptV3(r.Context(), binding, sink)
	if err != nil {
		return abort(err)
	}
	if _, err = flow.Control.AcknowledgeInitialInstallation(r.Context(), binding, observed); err != nil {
		return abort(err)
	}
	stage.Position = floatOrZeroHandlerV3(req.StartPosition)
	stage.AudioTrackIndex = audioIndex
	stage.DisableProgressPersistence = binding.Progress.PersistenceDisabled
	executor := playback.ExecutorNamespaceV3{Incarnation: binding.Fence.Incarnation, Epoch: binding.Fence.Epoch, ExecutorID: uuid.NewString()}
	stage.Executor = &executor
	stage.TranscodeTransportID = uuid.NewString()
	result.Plan.SessionID = stage.ID
	var card playback.RecipeCard
	var transcodeOpts playback.TranscodeOpts
	if isTranscode {
		decision := h.resolveHLSRouteWithPolicyV3(r.Context(), stage, result, h.playbackRoutingPolicyForContextV3(r.Context()), false, nil, nil)
		if decision.Outcome != noderouting.OutcomeSelected || decision.Shape.Execution != noderouting.ExecutionAPI || decision.Shape.Egress != noderouting.EgressAPI {
			if releaser, ok := h.NodePlanner.(sessionReservationReleaserV3); ok {
				releaser.ReleaseSession(stage.ID)
			}
			return abort(errors.New("initial flow requires integrated HLS execution and egress"))
		}
		card, transcodeOpts, err = h.prepareInitialLocalTranscodeV3(r.Context(), stage, effective, result)
		if err != nil {
			return abort(err)
		}
		stage.RoutingWorkload = card.RoutingWorkload
		stage.RoutingExecution = card.RoutingExecution
		stage.RoutingEgress = card.RoutingEgress
		stage.TranscodeHWAccel = transcodeOpts.HWAccel
		stage.ToneMapMode = transcodeOpts.ToneMapMode
		stage.TargetResolution = transcodeOpts.TargetResolution
		stage.TargetVideoCodec = transcodeOpts.TargetCodecVideo
		stage.TargetAudioCodec = transcodeOpts.TargetCodecAudio
		stage.SourceAudioChannels = transcodeOpts.SourceAudioChannels
		stage.TargetAudioChannels = transcodeOpts.TargetAudioChannels
		stage.TargetAudioBitrateKbps = transcodeOpts.TargetAudioBitrateKbps
		stage.TargetBitrateKbps = transcodeOpts.TargetBitrateKbps
		stage.StreamBitrateKbps = transcodeOpts.TargetBitrateKbps
		stage.SubtitleTrackIndex = transcodeOpts.SubtitleTrackIndex
		stage.SubtitleBurnIn = transcodeOpts.SubtitleBurnIn
		stage.SegmentDuration = transcodeOpts.SegmentDuration
	} else {
		decision, routeErr := h.resolveIdentityRouteV3(r, stage.ID, result, mediaAuthModeV3{}, h.playbackRoutingPolicyForContextV3(r.Context()), nil)
		if routeErr != nil {
			return abort(routeErr)
		}
		if decision.Shape.Execution != noderouting.ExecutionNone || decision.Shape.Egress != noderouting.EgressAPI {
			return abort(errors.New("initial flow cannot execute selected route"))
		}
		stage.RoutingWorkload = string(decision.Shape.Workload)
		stage.RoutingExecution = string(decision.Shape.Execution)
		stage.RoutingEgress = string(decision.Shape.Egress)
		card = playback.NewDirectRecipeCard(stage.ID, userID, profileID, effective.ID)
		card.Executor = &executor
		card.TranscodeTransportID = stage.TranscodeTransportID
		card.RoutingWorkload = stage.RoutingWorkload
		card.RoutingExecution = stage.RoutingExecution
		card.RoutingEgress = stage.RoutingEgress
	}
	token, err := streamtoken.Sign(card.ToClaims(), h.JWTSecret, playback.MaxTokenTTL)
	if err != nil {
		return abort(err)
	}
	result.Plan.SessionID = stage.ID
	result.Plan.Stream.URL = "/api/v1/stream/" + stage.ID + "?st=" + url.QueryEscape(token)
	if isTranscode {
		result.Plan.Stream.URL = "/api/v1/playback/transcode/" + stage.ID + "/master.m3u8?st=" + url.QueryEscape(token)
	}
	recipe, err := h.freezeExecutableRecipeV3(r.Context(), effective, result)
	if err != nil {
		return abort(err)
	}
	if result.SubtitleTrackIndex >= 0 && !result.SubtitleBurnIn {
		return abort(errors.New("initial subtitle delivery is not wired"))
	}
	response := playback.DecisionResponseV3{ProtocolVersion: playback.ProtocolV3, ServerFeatures: append(playback.ServerFeaturesV3(), "sequenced_progress_v1"), Outcome: playback.OutcomePlayableV3, SessionID: stage.ID, PlaybackPlan: result.Plan}
	record := playback.AttemptRecordV3{PlaybackAttemptID: req.PlaybackAttemptID, SessionID: stage.ID, UserID: userID, ProfileID: profileID, RequestedMediaFileID: requested.ID, EffectiveMediaFileID: effective.ID, CurrentPlanID: result.Plan.PlanID, CurrentPlan: *result.Plan, FrozenRecipe: recipe, NormalizedRequest: req, StartResponse: response, RequestDigest: digests.current, ExpiresAt: reservation.Record.ExpiresAt}
	route := playback.AttemptGrantRouteV3{Executor: executor, TransportID: stage.TranscodeTransportID}
	if err = flow.Control.StageAttemptRoute(r.Context(), owner.Authority(), record, route); err != nil {
		return abort(err)
	}
	locator, err := flow.Recipes.PutImmutable(r.Context(), card)
	if err != nil {
		return abort(err)
	}
	if err = flow.Control.PublishAttemptRecipeLocator(r.Context(), owner.Authority(), nil, locator); err != nil {
		return abort(err)
	}
	if err = owner.Check(); err != nil {
		return abort(err)
	}
	var runtime *playback.TranscodeSession
	if isTranscode {
		transcodeOpts.ExecuteGrants = flow.AcquireGrant
		var startup *localTransportStartupFailureV3
		runtime, startup = h.startReadyLocalPlaybackTransportV3(r.Context(), transcodeOpts)
		if startup != nil {
			return abort(startup.cause)
		}
		if !h.tm.SwapTranscodeSessionIf(stage.ID, nil, runtime) {
			_ = runtime.Close()
			return abort(errors.New("initial executor already registered"))
		}
		defer func() {
			if !retained {
				h.tm.CloseTranscodeSessionIf(stage.ID, runtime, "")
			}
		}()
		if err = owner.Check(); err != nil {
			return abort(err)
		}
	}
	retainStage = true
	if _, err = flow.Control.PublishInitialActivation(r.Context(), binding, record); err != nil {
		state, readErr := flow.Control.ReadInitialActivation(r.Context(), binding)
		if readErr != nil {
			retained = true
			h.retainInitialOwnerV3(binding, stage, owner, record)
			return fail(err)
		}
		if state.Phase != playback.InitialActivationActivatedV3 {
			retainStage = false
			return abort(err)
		}
	}
	// Durable publication precedes local visibility. Never roll back its sink.
	retained = true
	h.retainInitialOwnerV3(binding, stage, owner, record)
	if _, err = manager.PublishInitialSession(r.Context(), binding, *stage); err != nil {
		return fail(err)
	}
	published = true
	return response, nil
}

func (h *PlaybackHandler) reconcileInitialAbortV3(ctx context.Context, binding playback.InitialActivationBindingV3, abortID string) error {
	flow := h.initialFlow
	sink, err := flow.Sources.OpenPlaybackSink(ctx, binding.Source)
	if err != nil {
		return err
	}
	defer sink.Close() //nolint:errcheck
	if _, err = sink.InstallPlaybackAuthority(ctx, userstore.InstallPlaybackAuthorityRequest{Scope: binding.Scope, Next: binding.Fence}); err != nil {
		return err
	}
	if _, err = sink.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: binding.Scope, Fence: binding.Fence, StopID: abortID}); err != nil {
		return err
	}
	observed, err := playback.ReadInitialActivationReceiptV3(ctx, binding, sink)
	if err != nil {
		return err
	}
	_, err = flow.Control.CompleteInitialAbort(ctx, binding, abortID, observed)
	return err
}

type initialProgressRequestV3 struct {
	Sequence int64   `json:"sequence"`
	Position float64 `json:"position"`
	IsPaused bool    `json:"is_paused"`
}

type initialStopRequestV3 struct {
	StopID   string   `json:"stop_id"`
	Sequence int64    `json:"sequence"`
	Position *float64 `json:"position,omitempty"`
	IsPaused bool     `json:"is_paused"`
}

func (h *PlaybackHandler) handleInitialProgressV3(w http.ResponseWriter, r *http.Request) {
	flow := h.initialFlow
	active, err := flow.Control.GetActivatedPlaybackAuthority(r.Context(), apimw.GetUserID(r.Context()), apimw.GetProfileID(r.Context()), chi.URLParam(r, "session_id"))
	if err != nil || active.Activation.Phase != playback.InitialActivationActivatedV3 {
		writeNativeAuthorityUnavailable(w)
		return
	}
	var req initialProgressRequestV3
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlaybackV3BodyBytes)).Decode(&req); err != nil || req.Sequence <= 0 || req.Position < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "A positive progress sequence is required")
		return
	}
	ownerValue, ok := flow.owners.Load(active.Binding.Scope.SessionID)
	if !ok {
		writeNativeAuthorityUnavailable(w)
		return
	}
	owner, ok := ownerValue.(*playback.RuntimeOwnerLeaseV3)
	if !ok {
		writeNativeAuthorityUnavailable(w)
		return
	}
	pendingValue, ok := flow.pending.Load(active.Binding.Scope.SessionID)
	if !ok {
		writeNativeAuthorityUnavailable(w)
		return
	}
	pending, ok := pendingValue.(*initialPendingPublicationV3)
	if !ok {
		writeNativeAuthorityUnavailable(w)
		return
	}
	// Keep the local projection ordered with the sink result for this owner.
	// The sink remains authoritative across retries and other server processes.
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.binding != active.Binding || pending.owner != owner {
		writeNativeAuthorityUnavailable(w)
		return
	}
	if owner.Authority().OwnerID != active.Binding.Fence.OwnerID || owner.Check() != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	release := context.AfterFunc(owner.Context(), cancel)
	defer release()
	sink, err := flow.Sources.OpenPlaybackSink(ctx, active.Binding.Source)
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	defer sink.Close() //nolint:errcheck
	sample := active.Binding.Progress
	sample.Sequence = req.Sequence
	sample.PositionSeconds = req.Position
	sample.Paused = req.IsPaused
	result, err := sink.ApplyPlaybackProgress(ctx, userstore.ApplyPlaybackProgressRequest{Scope: active.Binding.Scope, Fence: active.Binding.Fence, Sample: sample})
	if err != nil {
		if errors.Is(err, userstore.ErrPlaybackSinkConflict) {
			writeError(w, http.StatusConflict, "progress_conflict", "The sequence already has different progress")
		} else {
			writeNativeAuthorityUnavailable(w)
		}
		return
	}
	if owner.Check() != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	if result.State.Last != nil {
		_ = h.sessionMgr.UpdateProgress(active.Binding.Scope.SessionID, result.State.Last.Sample.PositionSeconds, result.State.Last.Sample.Paused)
	}
	writeJSON(w, http.StatusOK, initialMutationResponse(result, false))
}

func (h *PlaybackHandler) handleInitialStopV3(w http.ResponseWriter, r *http.Request) {
	flow := h.initialFlow
	active, err := flow.Control.GetActivatedPlaybackAuthority(r.Context(), apimw.GetUserID(r.Context()), apimw.GetProfileID(r.Context()), chi.URLParam(r, "session_id"))
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	var req initialStopRequestV3
	if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlaybackV3BodyBytes)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "A stable stop ID is required")
		return
	}
	id, err := uuid.Parse(req.StopID)
	if err != nil || id == uuid.Nil || id.String() != req.StopID || req.Sequence < 0 || req.Position != nil && *req.Position < 0 || (req.Position == nil) != (req.Sequence == 0) {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid stop identity or final sample")
		return
	}
	state, err := flow.Control.BeginBoundStop(r.Context(), active.Binding, req.StopID)
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	if ownerValue, ok := flow.owners.LoadAndDelete(active.Binding.Scope.SessionID); ok {
		if owner, ok := ownerValue.(*playback.RuntimeOwnerLeaseV3); ok {
			owner.Close()
		}
	}
	if runtime := h.tm.GetTranscodeSession(active.Binding.Scope.SessionID); runtime != nil {
		opts := runtime.Opts()
		if opts.Executor != nil && opts.Executor.Incarnation == active.Binding.Fence.Incarnation && opts.Executor.Epoch == active.Binding.Fence.Epoch {
			h.tm.CloseTranscodeSessionIf(active.Binding.Scope.SessionID, runtime, "")
		}
	}
	sink, err := flow.Sources.OpenPlaybackSink(r.Context(), active.Binding.Source)
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	defer sink.Close() //nolint:errcheck
	var final *userstore.PlaybackProgressSample
	if req.Position != nil {
		sample := active.Binding.Progress
		sample.Sequence = req.Sequence
		sample.PositionSeconds = *req.Position
		sample.Paused = req.IsPaused
		final = &sample
	}
	var identity userstore.WatchIdentity
	if active.Binding.HistoryIdentityJSON != "" {
		if err = json.Unmarshal([]byte(active.Binding.HistoryIdentityJSON), &identity); err != nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
	}
	result, err := sink.StopPlaybackProgress(r.Context(), userstore.StopPlaybackProgressRequest{Scope: active.Binding.Scope, Fence: active.Binding.Fence, StopID: req.StopID, FinalSample: final, Identity: identity})
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	observed, err := playback.ReadInitialActivationReceiptV3(r.Context(), active.Binding, sink)
	if err != nil {
		writeNativeAuthorityUnavailable(w)
		return
	}
	if _, err = flow.Control.CompleteBoundStop(r.Context(), active.Binding, req.StopID, observed); err != nil {
		current, readErr := flow.Control.ReadInitialActivation(r.Context(), active.Binding)
		if readErr != nil || current.Phase != playback.InitialActivationStoppedV3 {
			if readErr == nil && current.Phase == playback.InitialActivationStoppingV3 && current.StopID == state.StopID {
				writeJSON(w, http.StatusAccepted, initialMutationResponse(result, true))
				return
			}
			writeNativeAuthorityUnavailable(w)
			return
		}
	}
	// Manager-only removal follows durable terminal state; no legacy writer runs.
	if err = h.sessionMgr.StopSession(active.Binding.Scope.SessionID); err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
		writeNativeAuthorityUnavailable(w)
		return
	}
	writeJSON(w, http.StatusOK, initialMutationResponse(result, false))
}

type initialAcceptedProgressV3 struct {
	Sequence int64   `json:"sequence"`
	Position float64 `json:"position"`
	IsPaused bool    `json:"is_paused"`
}

type initialMutationResponseV3 struct {
	Outcome   string                     `json:"outcome"`
	Accepted  *initialAcceptedProgressV3 `json:"accepted,omitempty"`
	StopID    string                     `json:"stop_id,omitempty"`
	HistoryID string                     `json:"history_id,omitempty"`
}

func initialMutationResponse(result userstore.PlaybackProgressResult, draining bool) initialMutationResponseV3 {
	response := initialMutationResponseV3{Outcome: result.Outcome}
	if result.State.Last != nil {
		sample := result.State.Last.Sample
		response.Accepted = &initialAcceptedProgressV3{Sequence: sample.Sequence, Position: sample.PositionSeconds, IsPaused: sample.Paused}
	}
	if result.State.Stop != nil {
		response.StopID = result.State.Stop.StopID
		if result.State.Stop.History != nil {
			response.HistoryID = result.State.Stop.History.ID
		}
	}
	if draining {
		response.Outcome = "draining"
	}
	return response
}
