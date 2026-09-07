package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

// Administrator terminate, v2 (decision option A).
//
// The frozen v1 handler dispatches a terminate command and, when the lane is
// silent, ends the session after a deadline: revocation follows the client.
// On v2 the order is reversed and each fact is reported on its own:
//
//  1. The server first revokes the session's playback authority durably. For
//     a session started through the v2 initial flow that is the same bound
//     stop the owner would perform (BeginBoundStop, sink stop, CompleteBoundStop)
//     under an administrator-owned stop identity, so the attempt row moves to
//     draining then stopped, grants are refused, the owner lease is closed, the
//     recipe runtime is closed, and the session is removed. For a bridge
//     session the manager stop and the ordinary finalizer revoke grants,
//     recipes and the synced session row. After this step media tokens for the
//     session are refused and progress writes are refused.
//  2. Then the client dismissal is dispatched as best effort over the existing
//     command lane. No wait for an acknowledgement.
//  3. The receipt carries authority_revoked and client_notified separately; it
//     never promises that buffered media stops instantly.
//
// Repeating terminate on a terminated bound session converges: the durable
// stop is idempotent under the administrator stop identity, and a session
// whose attempt row is terminal is reported as already revoked with
// client_notified false. A session with no durable row that is no longer held
// is unknown (404), as on the bridge.

const (
	AdminTerminateDeliveryDispatched  = "dispatched"
	AdminTerminateDeliveryUnavailable = "unavailable"
	AdminTerminateDeliveryFailed      = "failed"
	AdminTerminateDeliveryNone        = "none"

	// Durable states a revoked session can be in. Both refuse new grants and
	// progress; draining means outstanding serve grants have not expired yet
	// and the terminal receipt is committed by a later terminate, the owner's
	// stop, or reconciliation.
	AdminTerminateDurableDraining = "draining"
	AdminTerminateDurableStopped  = "stopped"
	AdminTerminateDurableNone     = "none"

	// adminTerminateStopNamespace derives one stable stop identity per
	// playback session, so a repeated terminate replays the same durable stop.
	adminTerminateStopNamespace = "silo:admin-terminate:"
)

// ErrAdminTerminateUnavailable means the durable revocation seam is not wired.
var ErrAdminTerminateUnavailable = errors.New("administrator terminate is unavailable")

// AdminTerminateInput is one administrator terminate.
type AdminTerminateInput struct {
	SessionID string
	ActorID   int
	Reason    string
}

// AdminTerminateView reports the two facts separately.
type AdminTerminateView struct {
	SessionID        string
	AuthorityRevoked bool
	AlreadyRevoked   bool
	DurableState     string
	ClientNotified   bool
	Delivery         string
	CommandID        string
}

// AdminTerminateAvailable reports whether durable revocation can be
// performed from this process: the session manager plus the bound-stop seam
// (initial flow control store) for v2 sessions and the ordinary stop path for
// bridge sessions. The command lane is optional; without it the client is
// simply not notified.
func (h *AdminPlaybackControlHandler) AdminTerminateAvailable() bool {
	return h != nil && h.playback != nil && h.playback.sessionMgr != nil && h.playback.initialFlow != nil && h.playback.initialFlow.Control != nil && h.playback.initialFlow.Sources != nil
}

// Terminate revokes first, then notifies best effort.
func (h *AdminPlaybackControlHandler) Terminate(ctx context.Context, in AdminTerminateInput) (AdminTerminateView, error) {
	if !h.AdminTerminateAvailable() {
		return AdminTerminateView{}, ErrAdminTerminateUnavailable
	}
	if in.SessionID == "" || in.ActorID <= 0 {
		return AdminTerminateView{}, ErrAdminPlaybackCommandInvalid
	}
	view := AdminTerminateView{SessionID: in.SessionID, Delivery: AdminTerminateDeliveryNone, DurableState: AdminTerminateDurableNone}

	// Terminates for one session serialize so a repeat observes the first
	// outcome instead of racing it.
	unlock := h.terminateLock(in.SessionID)
	defer unlock()

	session, err := h.playback.sessionMgr.GetSession(in.SessionID)
	if err != nil {
		if !errors.Is(err, playback.ErrSessionNotFound) {
			return AdminTerminateView{}, err
		}
		// Already gone from the manager. A bound session's durable state is
		// authoritative: confirm it is terminal before claiming convergence,
		// and complete a drained stop so the repeat converges on stopped.
		durable, terminal, err := h.durableTerminalState(ctx, in.SessionID)
		if err != nil {
			return AdminTerminateView{}, err
		}
		if !terminal {
			return AdminTerminateView{}, playback.ErrSessionNotFound
		}
		view.AuthorityRevoked, view.AlreadyRevoked, view.DurableState = true, true, durable
		return view, nil
	}

	// 1. Durable revocation.
	if _, bound := session.InitialActivationBinding(); bound {
		durable, err := h.revokeBoundSession(ctx, session)
		if err != nil {
			return AdminTerminateView{}, err
		}
		view.DurableState = durable
	} else {
		if err := h.playback.stopPlaybackSessionByID(ctx, session.ID, true); err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
			return AdminTerminateView{}, err
		}
		view.DurableState = AdminTerminateDurableStopped
	}
	view.AuthorityRevoked = true

	// 2. Best-effort client dismissal on the existing lane. The session is
	// already removed, so the dispatcher's own session lookup would refuse;
	// the hub lane may still be open until the client's socket notices.
	view.CommandID, view.Delivery, view.ClientNotified = h.notifyTerminated(in)
	return view, nil
}

// adminTerminateStopID derives the administrator stop identity for a session.
func adminTerminateStopID(sessionID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(adminTerminateStopNamespace+sessionID)).String()
}

// revokeBoundSession performs the owner's bound stop under the administrator
// stop identity, then removes the session. It returns the durable state:
// stopped when the terminal receipt committed, draining when outstanding
// serve grants have not expired yet (grants and progress are already refused;
// a later terminate, the owner's stop, or reconciliation completes it).
func (h *AdminPlaybackControlHandler) revokeBoundSession(ctx context.Context, session *playback.Session) (string, error) {
	pb := h.playback
	flow := pb.initialFlow
	active, err := flow.Control.GetActivatedPlaybackAuthority(ctx, session.UserID, session.ProfileID, session.ID)
	if err != nil {
		return "", playbackAuthorityOperationError()
	}
	stopID := adminTerminateStopID(session.ID)
	switch active.Activation.Phase {
	case playback.InitialActivationActivatedV3:
		if _, err := flow.Control.BeginBoundStop(ctx, active.Binding, stopID); err != nil {
			return "", playbackAuthorityOperationError()
		}
	case playback.InitialActivationStoppingV3:
		// A stop is already draining, under this identity or the owner's;
		// authority is already revoked. Fall through to completion.
	case playback.InitialActivationStoppedV3:
		_ = pb.sessionMgr.StopSession(session.ID)
		return AdminTerminateDurableStopped, nil
	default:
		return "", playbackAuthorityOperationError()
	}
	pb.closeInitialRuntimeV3(active.Binding)
	durable, err := h.completeBoundStop(ctx, active.Binding, stopID)
	if err != nil {
		return "", err
	}
	if err := pb.sessionMgr.StopSession(session.ID); err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
		return "", err
	}
	return durable, nil
}

// completeBoundStop writes the sink stop and commits the terminal receipt
// when the drain has elapsed. The stop identity already recorded on the row
// wins over the administrator's, so an owner-initiated drain completes too.
func (h *AdminPlaybackControlHandler) completeBoundStop(ctx context.Context, binding playback.InitialActivationBindingV3, stopID string) (string, error) {
	flow := h.playback.initialFlow
	current, err := flow.Control.ReadInitialActivation(ctx, binding)
	if err != nil {
		return "", playbackAuthorityOperationError()
	}
	if current.Phase == playback.InitialActivationStoppedV3 {
		return AdminTerminateDurableStopped, nil
	}
	if current.StopID != "" {
		stopID = current.StopID
	}
	sink, err := flow.Sources.OpenPlaybackSink(ctx, binding.Source)
	if err != nil {
		return "", playbackAuthorityOperationError()
	}
	defer sink.Close() //nolint:errcheck
	var identity userstore.WatchIdentity
	if binding.HistoryIdentityJSON != "" {
		if err := json.Unmarshal([]byte(binding.HistoryIdentityJSON), &identity); err != nil {
			return "", playbackAuthorityOperationError()
		}
	}
	if _, err := sink.StopPlaybackProgress(ctx, userstore.StopPlaybackProgressRequest{Scope: binding.Scope, Fence: binding.Fence, StopID: stopID, Identity: identity}); err != nil {
		return "", playbackAuthorityOperationError()
	}
	observed, err := playback.ReadInitialActivationReceiptV3(ctx, binding, sink)
	if err != nil {
		return "", playbackAuthorityOperationError()
	}
	if _, err := flow.Control.CompleteBoundStop(ctx, binding, stopID, observed); err != nil {
		state, readErr := flow.Control.ReadInitialActivation(ctx, binding)
		if readErr != nil {
			return "", playbackAuthorityOperationError()
		}
		switch {
		case state.Phase == playback.InitialActivationStoppedV3:
			return AdminTerminateDurableStopped, nil
		case state.Phase == playback.InitialActivationStoppingV3 && state.StopID == stopID:
			return AdminTerminateDurableDraining, nil
		}
		return "", playbackAuthorityOperationError()
	}
	return AdminTerminateDurableStopped, nil
}

// durableTerminalState reports whether a session absent from the manager is
// durably terminal, and completes a drained stop so the state converges on
// stopped. Only a bound session's attempt row can prove that; a session with
// no durable row and no live state is unknown here (404), the same answer the
// bridge gives for a session it no longer holds.
func (h *AdminPlaybackControlHandler) durableTerminalState(ctx context.Context, sessionID string) (durable string, terminal bool, err error) {
	flow := h.playback.initialFlow
	reader, ok := flow.Control.(interface {
		SessionActivationPhase(context.Context, string) (playback.SessionActivationPhaseV3, bool, error)
	})
	if !ok {
		return "", false, nil
	}
	row, found, err := reader.SessionActivationPhase(ctx, sessionID)
	if err != nil {
		return "", false, playbackAuthorityOperationError()
	}
	if !found {
		return "", false, nil
	}
	switch row.Phase {
	case playback.InitialActivationStoppedV3, playback.InitialActivationAbortedV3:
		return AdminTerminateDurableStopped, true, nil
	case playback.InitialActivationAbortingV3:
		return AdminTerminateDurableDraining, true, nil
	case playback.InitialActivationStoppingV3:
		active, err := flow.Control.GetActivatedPlaybackAuthority(ctx, row.UserID, row.ProfileID, sessionID)
		if err != nil {
			return AdminTerminateDurableDraining, true, nil
		}
		durable, err := h.completeBoundStop(ctx, active.Binding, active.Activation.StopID)
		if err != nil {
			return AdminTerminateDurableDraining, true, nil
		}
		return durable, true, nil
	}
	return "", false, nil
}

// notifyTerminated sends the terminate command on the lane if one is open.
func (h *AdminPlaybackControlHandler) notifyTerminated(in AdminTerminateInput) (commandID, delivery string, notified bool) {
	pb := h.playback
	if pb.RealtimeHub == nil {
		return "", AdminTerminateDeliveryUnavailable, false
	}
	commandID = uuid.NewString()
	command, err := playback.NewCommandEnvelope(in.SessionID, commandID, playback.CommandTerminate, nil)
	if err != nil {
		return commandID, AdminTerminateDeliveryFailed, false
	}
	command.Reason = in.Reason
	command.IssuedBy = &playback.CommandIssuedBy{Kind: eventsAdminRole}
	command.DeadlineMS = int(defaultPlaybackControlDeadline / time.Millisecond)
	if err := pb.RealtimeHub.Send(in.SessionID, command); err != nil {
		if errors.Is(err, playback.ErrRealtimeConnectionNotFound) {
			return commandID, AdminTerminateDeliveryUnavailable, false
		}
		slog.Warn("terminate notification failed after revocation", "session", in.SessionID, "playback_session_id", in.SessionID, "error", err)
		return commandID, AdminTerminateDeliveryFailed, false
	}
	return commandID, AdminTerminateDeliveryDispatched, true
}

func (h *AdminPlaybackControlHandler) terminateLock(sessionID string) func() {
	h.terminateMu.Lock()
	if h.terminateLocks == nil {
		h.terminateLocks = map[string]*sync.Mutex{}
	}
	mu := h.terminateLocks[sessionID]
	if mu == nil {
		mu = &sync.Mutex{}
		h.terminateLocks[sessionID] = mu
	}
	h.terminateMu.Unlock()
	mu.Lock()
	return mu.Unlock
}
