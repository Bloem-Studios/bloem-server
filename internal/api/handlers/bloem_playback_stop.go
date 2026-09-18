package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

// wireRemotePlaybackStop withdraws this replica's producer without replaying
// the history/finalization side effects already committed by the stopping peer.
func (h *PlaybackHandler) wireRemotePlaybackStop() {
	manager, ok := h.sessionMgr.(interface{ AddRemoteStopHook(func(*playback.Session)) })
	if !ok {
		return
	}
	manager.AddRemoteStopHook(func(session *playback.Session) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.cancelPlaybackStartSideEffectsV3(ctx, session.ID)
		h.notifyRemoteSessionEnded(ctx, session.ID)
		h.closeTranscodeForSession(session)
		h.deleteProxyGrantV3(ctx, session.ID)
		h.deleteNodeRecipeV3(ctx, session.TranscodeTransportID)
	})
}

// stopDurablePlayback uses the existing sequenced stop/receipt authority even
// for bridge clients which do not send stop IDs. Only sessions without an
// attempt row fall back to the legacy in-memory path. A store failure is not
// evidence of absence and must not bypass durable revocation.
func (h *PlaybackHandler) stopDurablePlayback(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	store, ok := h.PlanStoreV3.(playback.ProgressStoreV3)
	if !ok {
		return false
	}
	// Durable attempt IDs are UUIDs. A legacy/non-UUID identifier cannot have
	// a PostgreSQL attempt row; let the existing in-memory path resolve it
	// rather than turning an invalid UUID cast into a dependency outage.
	id, err := uuid.Parse(sessionID)
	if err != nil {
		return false
	}
	sessionID = id.String()
	record, err := h.PlanStoreV3.GetAttempt(r.Context(), sessionID)
	if errors.Is(err, playback.ErrSessionNotFound) {
		return false
	}
	if err != nil || record == nil {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "Playback stop is temporarily unavailable")
		return true
	}
	if !callerOwnsPlaybackSession(r, record.UserID, record.ProfileID, apimw.GetUserID(r.Context())) {
		writeError(w, http.StatusForbidden, "forbidden", "Session belongs to another user")
		return true
	}
	receipt, _, err := store.StopAttempt(r.Context(), sessionID, uuid.NewString(), nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "Playback stop is temporarily unavailable")
		return true
	}
	if !receipt.Finalized {
		h.finalizeStopV2(r.Context(), store, record, sessionID, receipt)
	}
	h.forgetProgressSideEffectLock(sessionID)
	w.WriteHeader(http.StatusNoContent)
	return true
}
