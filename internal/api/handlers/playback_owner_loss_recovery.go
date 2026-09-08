package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// PlaybackOwnerLossReceipt describes abandonment of the original attempt. Its
// accepted sample is a stored source receipt, never acknowledgement of a new STOP.
type PlaybackOwnerLossReceipt struct {
	RecoveryID        string                    `json:"recovery_id"`
	PlaybackAttemptID string                    `json:"playback_attempt_id"`
	SessionID         string                    `json:"session_id"`
	State             string                    `json:"state"`
	Reason            string                    `json:"reason"`
	Accepted          *PlaybackAcceptedProgress `json:"accepted,omitempty"`
}

type PlaybackOwnerLossPending struct {
	Outcome  string                   `json:"outcome"`
	Recovery PlaybackOwnerLossReceipt `json:"recovery"`
}

type PlaybackOwnerLossStart struct {
	playback.DecisionResponseV3
	Recovery PlaybackOwnerLossReceipt `json:"recovery"`
}

// RecoverInitialPlaybackStart computes the same body/device digest as ordinary
// START before any catalog or boot-local live-session lookup.
func (h *PlaybackHandler) RecoverInitialPlaybackStart(ctx context.Context, caller PlaybackCaller, request playback.StartRequestV3) (int, any, bool, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return 0, nil, true, err
	}
	var validated playback.StartRequestV3
	if err = json.Unmarshal(body, &validated); err != nil {
		return 0, nil, true, err
	}
	if _, err = validated.NormalizeAndValidate(); err != nil {
		return 0, nil, true, err
	}
	if validated.ProfileID != caller.ProfileID {
		return 0, nil, true, playback.ErrInitialActivationConflictV3
	}
	deviceID := deviceMetadataFromRequest(playbackCallerRequest(ctx, caller)).DeviceID
	return h.ResolvePlaybackOwnerLoss(ctx, caller, playback.InitialRecoveryLookupV3{AttemptID: request.PlaybackAttemptID, RequestDigest: newPlaybackStartRequestDigestsV3(body, deviceID).current})
}

// ResolvePlaybackOwnerLoss runs before ordinary live-owner START/STOP lookup.
// START supplies the original attempt and server-computed normalized digest;
// STOP supplies its session. Authenticated identity always overrides lookup data.
// handled=false preserves live-owner and already-started client StopID paths.
func (h *PlaybackHandler) ResolvePlaybackOwnerLoss(ctx context.Context, caller PlaybackCaller, lookup playback.InitialRecoveryLookupV3) (status int, body any, handled bool, err error) {
	if err = h.validatePlaybackCaller(ctx, caller); err != nil {
		return 0, nil, true, err
	}
	if h.initialFlow == nil {
		return 0, nil, false, nil
	}
	store, ok := h.initialFlow.Control.(playback.OwnerLossRecoveryStoreV3)
	if !ok {
		return 0, nil, false, nil
	}
	lookup.AccountID, lookup.ProfileID = caller.UserID, caller.ProfileID
	state, err := store.LookupInitialRecovery(ctx, lookup)
	if errors.Is(err, playback.ErrSessionNotFound) {
		return 0, nil, false, nil
	}
	if errors.Is(err, playback.ErrIdempotencyKeyReusedV3) {
		return 0, nil, true, playbackPersistenceOperationError(err)
	}
	if err != nil {
		return 0, nil, true, err
	}
	if lookup.SessionID != "" && lookup.TimelineID != state.Binding.ClientTimeline.TimelineID {
		return 0, nil, true, playbackOperationError(http.StatusConflict, "timeline_changed", "Playback timeline does not match the captured session")
	}
	state, err = h.reconcileOwnerLossV3(ctx, state.Binding)
	if errors.Is(err, playback.ErrInitialOwnerLiveV3) {
		return 0, nil, false, nil
	}
	if err != nil {
		return 0, nil, true, err
	}
	if state.AbortReason != playback.InitialAbortOwnerLostV3 {
		return 0, nil, false, nil
	}
	receipt := PlaybackOwnerLossReceipt{RecoveryID: state.AbortID, PlaybackAttemptID: state.Binding.Fence.AttemptID, SessionID: state.Binding.Scope.SessionID, State: "draining", Reason: playback.InitialAbortOwnerLostV3}
	if state.Phase != playback.InitialActivationAbortedV3 {
		return http.StatusAccepted, PlaybackOwnerLossPending{Outcome: "draining", Recovery: receipt}, true, nil
	}
	receipt.State = "aborted"
	if state.Terminal == nil {
		return 0, nil, true, playback.ErrInitialActivationConflictV3
	}
	view, err := initialTimelineMutationResponse(state.Binding, userstore.PlaybackProgressResult{State: *state.Terminal}, false)
	if err != nil {
		return 0, nil, true, err
	}
	receipt.Accepted = view.Accepted
	if lookup.AttemptID == "" {
		return http.StatusOK, PlaybackOwnerLossPending{Outcome: "aborted", Recovery: receipt}, true, nil
	}
	return http.StatusCreated, PlaybackOwnerLossStart{DecisionResponseV3: playback.DecisionResponseV3{ProtocolVersion: playback.ProtocolV3, ServerFeatures: initialServerFeaturesV3(), Outcome: playback.OutcomeAdaptationUnavailableV3, Terminal: &playback.TerminalV3{Reason: "playback_owner_lost", Message: "Playback ended after its server owner was lost.", Retryable: false}}, Recovery: receipt}, true, nil
}

func (h *PlaybackHandler) reconcileOwnerLossV3(ctx context.Context, binding playback.InitialActivationBindingV3) (playback.InitialActivationV3, error) {
	store, ok := h.initialFlow.Control.(playback.OwnerLossRecoveryStoreV3)
	if !ok {
		return playback.InitialActivationV3{}, playback.ErrInitialActivationUnavailableV3
	}
	state, err := playback.ReconcileOwnerLossRecoveryV3(ctx, store, h.initialFlow.Sources, binding)
	if err != nil || state.AbortReason != playback.InitialAbortOwnerLostV3 {
		return state, err
	}
	h.closeInitialRuntimeV3(binding)
	if state.Phase == playback.InitialActivationAbortedV3 {
		if h.sessionMgr != nil {
			err = h.sessionMgr.StopSession(binding.Scope.SessionID)
			if errors.Is(err, playback.ErrSessionNotFound) {
				err = nil
			}
		}
		if releaser, ok := h.NodePlanner.(sessionReservationReleaserV3); ok {
			releaser.ReleaseSession(binding.Scope.SessionID)
		}
	}
	return state, err
}
