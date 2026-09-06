package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// InitialPlaybackDelivery exposes only executor-bound delivery through v2.
// The existing transport still validates the signed recipe, exact namespace,
// source grant and viewer before serving bytes. Legacy sessions cannot enter
// this route when their token has no executor binding.
func (h *PlaybackHandler) InitialPlaybackDelivery(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil || h.initialFlow == nil || next == nil {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "Initial playback is not configured")
			return
		}
		card, _ := verifiedStreamCardFromToken(r.URL.Query().Get(streamTokenParam), chi.URLParam(r, "session_id"), h.JWTSecret)
		if card == nil || card.Executor == nil {
			writeNativeAuthorityUnavailable(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
