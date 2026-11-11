package handlers

import (
	"context"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
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
