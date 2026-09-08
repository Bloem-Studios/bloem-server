package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// InitialPlaybackDelivery exposes only executor-bound delivery through v2.
// The transport validates a signed reference or the header-authenticated current
// session, then the immutable recipe, exact namespace, source grant and viewer.
// A legacy session cannot enter this route.
func (h *PlaybackHandler) InitialPlaybackDelivery(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.initialFlow == nil || h.sessionMgr == nil || next == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Initial playback is not configured")
			return
		}
		card, _ := initialMediaRecipeV3(r, h.tm, h.sessionMgr.GetSession, chi.URLParam(r, "session_id"), h.JWTSecret)
		if card == nil || card.Executor == nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
