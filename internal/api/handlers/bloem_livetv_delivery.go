package handlers

import "github.com/Silo-Server/silo-server/internal/livetv"

// BloemLiveHLSStreamPurpose separates native Live TV delivery from VOD and
// download proofs using the existing stream token's signed purpose field.
const BloemLiveHLSStreamPurpose = "bloem_livetv_hls"

// NewBloemLiveTVHandler keeps native links and delivery credentials out of the
// frozen legacy handler contract and the service's internal playback locators.
func NewBloemLiveTVHandler(service *livetv.Service, secret string) *LiveTVHandler {
	h := NewLiveTVHandler(service)
	if h != nil {
		h.native, h.JWTSecret = true, secret
	}
	return h
}

func (h *LiveTVHandler) bloemSessionDeliveryURL(session *livetv.LiveSession, userID int, profileID string) string {
	// Build a canonical same-origin URL, never copy a bridge/tuner URL's
	// authority or query. Only the HLS session's own ID can receive a ticket.
	if livetv.IsClientSafePlayURL(session.HLSURL) {
		if id, ok := livetv.LiveHLSDeliveryID(session.HLSURL); ok && id == session.PlaybackSessionID {
			path := NativeAPIPrefix + "/livetv/live-hls/" + id + "/index.m3u8"
			return appendStreamToken(path, h.signLiveStreamToken(id, userID, profileID))
		}
	}
	return NativeAPIPrefix + "/livetv/sessions/" + session.ID + "/stream"
}
