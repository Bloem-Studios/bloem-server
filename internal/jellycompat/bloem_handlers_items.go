package jellycompat

import (
	"context"
	"net/http"
)

// bloemItemsLiveTV is ItemsHandler's Live TV wiring, embedded so upstream's
// field list stays untouched.
type bloemItemsLiveTV struct {
	liveTVEnabled bool
	liveTV        *LiveTVHandler
}

// itemFromDetailForSession applies the media-facing part of the resolved
// policy after the shared mapper has built the Jellyfin DTO. CanDownload is a
// direct-play transport capability in this protocol, but the same route also
// downloads the original file. Consequently both playback and download policy
// must allow it. Custom playback-on/download-off plans are incompatible with
// Infuse direct play through this Jelly transport and advertise false.
func (h *ItemsHandler) itemFromDetailForSession(ctx context.Context, session *Session, item upstreamItemDetail, isFavorite bool, progress *upstreamProgress, requestedFields map[string]bool) baseItemDTO {
	dto := h.mapper.itemFromDetailWithFields(item, isFavorite, progress, requestedFields)
	if dto.CanDownload && h.accessFilter != nil && session != nil {
		filter := h.accessFilter(ctx, session.StreamAppUserID, session.ProfileID)
		dto.CanDownload = !filter.PlaybackDenied && !filter.DownloadDenied
	}
	return dto
}

// SetLiveTV wires the Live TV collection into compatible clients.
func (h *ItemsHandler) SetLiveTV(handler *LiveTVHandler) {
	h.liveTV = handler
	h.liveTVEnabled = handler != nil
}

// appendLiveTVView adds the Live TV collection view to userViews when Live
// TV is wired and the session may use it.
func (h *ItemsHandler) appendLiveTVView(ctx context.Context, session *Session, items []baseItemDTO) []baseItemDTO {
	if h.liveTVEnabled && h.liveTV != nil && h.liveTV.allowed(ctx, session) {
		items = append(items, h.liveTV.liveTVView())
	}
	return items
}

// serveLiveTVItems answers GET /Items?ParentId=<Live TV view> with the
// channel list and reports whether it handled the request.
func (h *ItemsHandler) serveLiveTVItems(w http.ResponseWriter, r *http.Request) bool {
	if isLiveTVViewID(newCaseInsensitiveQuery(r.URL.Query()).Get("ParentId")) && h.liveTV != nil {
		h.liveTV.HandleChannels(w, r)
		return true
	}
	return false
}

// serveLiveTVItem answers GET /Items/{id} for the Live TV view and channel
// IDs and reports whether it handled the request.
func (h *ItemsHandler) serveLiveTVItem(w http.ResponseWriter, r *http.Request, rawID string) bool {
	if isLiveTVViewID(rawID) && h.liveTV != nil {
		if !h.liveTV.requireAccess(w, r) {
			return true
		}
		writeJSON(w, http.StatusOK, h.liveTV.liveTVView())
		return true
	}
	if h.liveTV != nil {
		if _, ok := h.liveTV.DecodeLiveTVChannelID(rawID); ok {
			h.liveTV.HandleChannel(w, r)
			return true
		}
	}
	return false
}
