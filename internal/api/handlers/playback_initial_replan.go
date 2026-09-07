package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// BoundReplanStoreV3 is the fenced replan seam the initial flow uses. The legacy
// replan writer refuses authority-owned rows; these two operations admit exactly
// the captured owner generation and leave the executor route and retention
// deadline untouched.
type BoundReplanStoreV3 interface {
	BeginBoundReplan(context.Context, playback.AttemptAuthorityV3, string, string, string, string, time.Time) (playback.ReplanLeaseV3, error)
	CompleteBoundReplan(context.Context, playback.AttemptAuthorityV3, string, string, string, string, json.RawMessage, playback.AttemptRecordV3) error
}

// PlaybackReplanCommand is the typed v2 replan intent. The body keeps the v3
// wire shape; the adapter validates it before the flow sees it.
type PlaybackReplanCommand struct {
	Request playback.ReplanRequestV3
	Digest  string
}

// ReplanInitialPlayback preserves the captured owner and source while a seek
// prepares one successor executor. Durable retirement and cutover precede the
// local session projection. Track, quality and output intent changes remain
// separate planning operations.
func (h *PlaybackHandler) ReplanInitialPlayback(ctx context.Context, caller PlaybackCaller, sessionID string, command PlaybackReplanCommand) (playback.DecisionResponseV3, error) {
	if err := h.validatePlaybackCaller(ctx, caller); err != nil {
		return playback.DecisionResponseV3{}, err
	}
	if err := validatePlaybackSessionID(sessionID); err != nil {
		return playback.DecisionResponseV3{}, err
	}
	req := command.Request
	if command.Digest == "" || req.PlaybackAttemptID == "" || req.ReplanRequestID == "" {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusBadRequest, "bad_request", "Invalid replan request")
	}
	store, ok := h.initialFlow.Control.(BoundReplanStoreV3)
	if !ok {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusNotImplemented, "capability_unsupported", "Replanning is not supported by this playback authority store")
	}
	flow := h.initialFlow
	active, err := flow.Control.GetActivatedPlaybackAuthority(ctx, caller.UserID, caller.ProfileID, sessionID)
	if err != nil {
		if errors.Is(err, playback.ErrSessionNotFound) {
			return playback.DecisionResponseV3{}, playbackOperationError(http.StatusNotFound, "session_not_found", "Playback session not found")
		}
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	if active.Activation.Phase != playback.InitialActivationActivatedV3 {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "session_not_active", "The playback session is not active")
	}
	if active.Binding.Fence.AttemptID != req.PlaybackAttemptID {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "stale_playback_plan", "The failed plan is no longer current")
	}
	ownerValue, ok := flow.owners.Load(sessionID)
	if !ok {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	owner, ok := ownerValue.(*playback.RuntimeOwnerLeaseV3)
	if !ok || owner.Authority().OwnerID != active.Binding.Fence.OwnerID || owner.Authority().Epoch != active.Binding.Fence.Epoch || owner.Check() != nil {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	pendingValue, ok := flow.pending.Load(sessionID)
	if !ok {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	pending, ok := pendingValue.(*initialPendingPublicationV3)
	if !ok {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	// The pending mutex serializes this replan with progress, stop and the
	// owner-loss discard on the same boot; the store lease serializes it with
	// any other process that could hold the same fence.
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if pending.binding != active.Binding || pending.owner != owner {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	record := pending.record
	if record.PlaybackAttemptID != req.PlaybackAttemptID || record.SessionID != sessionID {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	release := context.AfterFunc(owner.Context(), cancel)
	defer release()
	if key, ok := pending.cancelledSuccessors[req.ReplanRequestID]; ok {
		if key.Digest != command.Digest {
			return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "idempotency_key_reused", "The replan request ID was reused with different input")
		}
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	if pending.successor != nil {
		key := pending.successor.document.Key
		if key.RequestID == req.ReplanRequestID {
			if key.Digest != command.Digest {
				return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "idempotency_key_reused", "The replan request ID was reused with different input")
			}
			return h.resumeInitialSuccessorV3(ctx, pending, pending.successor)
		}
		if !h.releaseCancelledInitialSuccessorV3(ctx, pending, pending.successor) {
			return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "replan_in_progress", "A retained route replacement must finish before another replan")
		}
	}
	authority := owner.Authority()
	lease, err := store.BeginBoundReplan(ctx, authority, sessionID, req.ReplanRequestID, command.Digest, record.CurrentReplanRequestID, time.Now().Add(replanLeaseDurationV3))
	switch {
	case errors.Is(err, playback.ErrIdempotencyKeyReusedV3):
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "idempotency_key_reused", "The replan request ID was reused with different input")
	case errors.Is(err, playback.ErrStaleReplanLeaseV3):
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "stale_playback_plan", "A newer replacement plan is already active")
	case errors.Is(err, playback.ErrStaleAttemptAuthorityV3), errors.Is(err, playback.ErrSessionNotFound):
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	case err != nil:
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	switch lease.State {
	case playback.ReplanLeaseInFlightV3:
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "replan_in_progress", "An identical replan is still in progress")
	case playback.ReplanLeaseCompletedV3:
		if record.CurrentReplanRequestID != req.ReplanRequestID || !completedReplanResponseMatchesAttemptV3(lease.Response, &record) {
			return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "stale_playback_plan", "A newer replacement plan is already active")
		}
		var replay playback.DecisionResponseV3
		if err := json.Unmarshal(lease.Response, &replay); err != nil {
			return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
		}
		return replay, nil
	}
	leaseCompleted := false
	defer func() {
		if leaseCompleted {
			return
		}
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), replanReleaseTimeoutV3)
		defer cancel()
		_ = h.PlanStoreV3.ReleaseReplan(releaseCtx, sessionID, req.ReplanRequestID, lease.LeaseToken)
	}()
	if record.CurrentPlanID != req.FailedPlanID {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "stale_playback_plan", "The failed plan is no longer current")
	}
	if err := validateInitialReplanIntentV3(&record, req); err != nil {
		return playback.DecisionResponseV3{}, err
	}
	session, err := h.sessionMgr.GetSession(sessionID)
	if err != nil || session == nil || session.Executor == nil {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	if session.Executor.Incarnation != active.Binding.Fence.Incarnation || session.Executor.Epoch != active.Binding.Fence.Epoch {
		return playback.DecisionResponseV3{}, playbackAuthorityOperationError()
	}
	result, err := frozenSeekReanchorResultV3(&record, req.PositionSeconds, time.Now())
	if err != nil {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "seek_reanchor_recipe_unavailable", "The active playback recipe cannot be reopened; start a new playback attempt")
	}
	if err := validateSeekReanchorPlanV3(&record, result.Plan); err != nil {
		return playback.DecisionResponseV3{}, playbackOperationError(http.StatusConflict, "seek_reanchor_route_changed", err.Error())
	}
	result.Plan.SessionID = sessionID
	successor, err := h.prepareInitialSuccessorV3(ctx, pending, session, req, command.Digest, lease, result)
	if err != nil {
		return playback.DecisionResponseV3{}, err
	}
	// Retain before staging or launch. Subsequent uncertain calls use this exact
	// key and namespace; release cannot erase a retained candidate document.
	pending.successor = successor
	response, err := h.resumeInitialSuccessorV3(ctx, pending, successor)
	if err == nil {
		leaseCompleted = true
	}
	return response, err
}

// validateInitialReplanIntentV3 admits a position re-anchor using the captured
// media recipe and selected nodes. Track, quality and output changes require
// separate planning and are refused before a candidate is prepared.
func validateInitialReplanIntentV3(record *playback.AttemptRecordV3, req playback.ReplanRequestV3) error {
	switch req.EffectiveOperation() {
	case playback.ReplanOperationSeekReanchorV3, playback.ReplanOperationSeekFailureRecoveryV3, playback.ReplanOperationFailureRecoveryV3:
	default:
		return playbackOperationError(http.StatusNotImplemented, "capability_unsupported", "The initial playback flow cannot change tracks, quality or output; start a new attempt")
	}
	if err := validateSeekRecoveryRequestV3(record, req); err != nil {
		return playbackOperationError(http.StatusNotImplemented, "capability_unsupported", err.Error())
	}
	if req.ClientFeatures != nil {
		pinned := playback.PinAttemptStickyFeaturesV3(req.ClientFeatures, record.NormalizedRequest.ClientFeatures)
		if strings.Join(pinned, ",") != strings.Join(req.ClientFeatures, ",") {
			return playbackOperationError(http.StatusNotImplemented, "capability_unsupported", "Attempt-sticky client features cannot change mid-attempt")
		}
	}
	if len(req.LocalMutations) > 0 {
		return playbackOperationError(http.StatusNotImplemented, "capability_unsupported", "Local plan mutations are not supported by the initial playback flow")
	}
	duration := record.FrozenRecipe.SourceDurationSeconds
	if duration > 0 && req.PositionSeconds > duration {
		return playbackOperationError(http.StatusUnprocessableEntity, "invalid_seek_position", "The requested position is beyond the end of the selected media source")
	}
	return nil
}

// ReplanDigestV3 fingerprints the exact replan body so a reused request id with
// different input is a detectable idempotency violation.
func ReplanDigestV3(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
