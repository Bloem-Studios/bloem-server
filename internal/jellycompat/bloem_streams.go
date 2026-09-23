package jellycompat

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// downloadDenied applies the account/profile access policy to
// /Items/{id}/Download and writes the 403 when it refuses. The route serves
// the original file and Infuse also uses it for Direct Play, so a custom
// playback-on/download-off policy cannot use that client transport;
// offline-download security takes precedence.
func (h *PlaybackHandler) downloadDenied(w http.ResponseWriter, r *http.Request, session *Session) bool {
	if h.accessFilter == nil {
		return false
	}
	filter := h.accessFilter(r.Context(), session.StreamAppUserID, session.ProfileID)
	if filter.PlaybackDenied {
		writeError(w, http.StatusForbidden, "Forbidden", "Playback is not allowed")
		return true
	}
	if filter.DownloadDenied {
		writeError(w, http.StatusForbidden, "Forbidden", "Downloads are not allowed")
		return true
	}
	return false
}

// admitCompatTransport re-resolves policy on every transport admission in
// ensureUpstreamPlayback. A durable compat session may outlive an entitlement
// reconciliation, so trusting only the policy that existed when it was
// created would let Browse-only users continue.
func (h *PlaybackHandler) admitCompatTransport(ctx context.Context, compatSession *Session) error {
	if compatSession == nil {
		return ErrSessionNotFound
	}
	if h.accessFilter != nil && h.accessFilter(ctx, compatSession.StreamAppUserID, compatSession.ProfileID).PlaybackDenied {
		return playback.ErrPlaybackNotAllowed
	}
	return nil
}
