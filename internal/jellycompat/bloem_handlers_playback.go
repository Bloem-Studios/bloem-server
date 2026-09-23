package jellycompat

import (
	"context"
	"net/http"
	"strings"

	"github.com/Silo-Server/silo-server/internal/config"
)

// SetLiveTV wires Live TV channel playback negotiation.
func (h *PlaybackHandler) SetLiveTV(handler *LiveTVHandler) { h.liveTV = handler }

// strictReconstructAdmission reports the operator's admission posture for a
// reconstruct whose limit provider could not be evaluated. Defaults to upstream
// Silo's fail-open behavior when the setting is unset or unreadable — a
// settings-store outage must not itself become the reason playback is refused.
func (h *PlaybackHandler) strictReconstructAdmission() bool {
	if h.SettingsRepo == nil {
		return false
	}
	v, err := h.SettingsRepo.Get(context.Background(), config.PlaybackStrictReconstructAdmissionSettingKey)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// serveLiveTVPlaybackInfo answers PlaybackInfo for a Live TV channel route ID
// and reports whether it handled the request. Non-channel IDs, or a handler
// with Live TV unwired, fall through to upstream's item path.
func (h *PlaybackHandler) serveLiveTVPlaybackInfo(w http.ResponseWriter, r *http.Request, session *Session, routeID string) bool {
	if h.liveTV == nil {
		return false
	}
	if _, ok := h.liveTV.DecodeLiveTVChannelID(routeID); !ok {
		return false
	}
	req, _, err := h.parsePlaybackRequest(r, session.Token)
	if err != nil {
		writeDeviceProfileRequestError(w, err, "Invalid playback request")
		return true
	}
	if req.UserID != "" && req.UserID != session.PseudoUserID.String() {
		writeError(w, http.StatusNotFound, "NotFound", "User not found")
		return true
	}
	autoOpen := req.AutoOpenLiveStream || r.URL.Query().Get("AutoOpenLiveStream") == "true"
	liveStreamID := firstNonEmpty(req.LiveStreamID, r.URL.Query().Get("LiveStreamId"))
	source, err := h.liveTV.PlaybackMediaSource(r.Context(), session, routeID, autoOpen, liveStreamID)
	if err != nil {
		writeLiveTVCompatError(w, err)
		return true
	}
	playSessionID := h.codec.EncodeStringID(EncodedIDPlaySession, uuidNewString())
	writeJSON(w, http.StatusOK, playbackInfoResponseDTO{
		PlaySessionID: playSessionID,
		MediaSources:  []mediaSourceDTO{source},
	})
	return true
}
