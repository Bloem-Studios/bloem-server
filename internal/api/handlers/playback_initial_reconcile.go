package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

type InitialPlaybackReconciliation struct {
	Visited, Completed, Pending int
	NextAttemptID               string
}

// ReconcileInitialPlayback processes one explicit account's retained intents.
// It never enrolls sources, adopts active owners, or invents a final stop sample.
func (h *PlaybackHandler) ReconcileInitialPlayback(ctx context.Context, accountID int, afterAttemptID string, limit int) (InitialPlaybackReconciliation, error) {
	var result InitialPlaybackReconciliation
	if h.initialFlow == nil {
		return result, playback.ErrInitialActivationUnavailableV3
	}
	inventory, ok := h.initialFlow.Control.(playback.InitialReconciliationStoreV3)
	if !ok {
		return result, playback.ErrInitialActivationUnavailableV3
	}
	states, err := inventory.ListInitialReconciliation(ctx, accountID, afterAttemptID, limit)
	if err != nil {
		return result, err
	}
	var failures []error
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Visited++
		result.NextAttemptID = state.Binding.Fence.AttemptID
		switch state.Phase {
		case playback.InitialActivationPendingV3, playback.InitialActivationInstalledV3:
			state, err = h.initialFlow.Control.AbortInitialActivation(ctx, state.Binding, uuid.NewString())
			if err == nil {
				err = h.reconcileInitialAbortV3(ctx, state.Binding, state.AbortID)
			}
		case playback.InitialActivationAbortingV3:
			err = h.reconcileInitialAbortV3(ctx, state.Binding, state.AbortID)
		case playback.InitialActivationStoppingV3:
			err = h.reconcileInitialStopReceipt(ctx, state)
		default:
			err = playback.ErrInitialActivationConflictV3
		}
		if err != nil {
			result.Pending++
			failures = append(failures, err)
			continue
		}
		result.Completed++
	}
	return result, errors.Join(failures...)
}

func (h *PlaybackHandler) reconcileInitialStopReceipt(ctx context.Context, state playback.InitialActivationV3) error {
	sink, err := h.initialFlow.Sources.OpenPlaybackSink(ctx, state.Binding.Source)
	if err != nil {
		return err
	}
	defer sink.Close() //nolint:errcheck
	receipt, err := playback.ReadInitialActivationReceiptV3(ctx, state.Binding, sink)
	if err != nil {
		return err
	}
	// Without a source receipt, only the original persisted client request can
	// finish its exact stop payload. Reconciliation cannot replace that payload.
	if _, err = h.initialFlow.Control.CompleteBoundStop(ctx, state.Binding, state.StopID, receipt); err != nil {
		return err
	}
	h.closeInitialRuntimeV3(state.Binding)
	err = h.sessionMgr.StopSession(state.Binding.Scope.SessionID)
	if err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
		return err
	}
	if releaser, ok := h.NodePlanner.(sessionReservationReleaserV3); ok {
		releaser.ReleaseSession(state.Binding.Scope.SessionID)
	}
	return nil
}

// RunInitialPlaybackReconciliation is explicitly scoped by the embedding test
// instance. Every tick visits at most one page per configured account. Failed
// intents remain durable and recur on the next sweep; no new authority is minted.
func (h *PlaybackHandler) RunInitialPlaybackReconciliation(ctx context.Context, accounts []int, interval time.Duration) {
	if ctx == nil || interval <= 0 || len(accounts) == 0 {
		return
	}
	cursors := make(map[int]string, len(accounts))
	for _, accountID := range accounts {
		if accountID > 0 {
			cursors[accountID] = ""
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		for accountID, cursor := range cursors {
			result, _ := h.ReconcileInitialPlayback(ctx, accountID, cursor, 100)
			if result.Visited < 100 {
				cursors[accountID] = ""
			} else {
				cursors[accountID] = result.NextAttemptID
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
