package compatapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Playback operations plan, authorize, and track playback. Media bytes never
// travel through this API: successful starts carry short-lived,
// audience-bound, same-origin delivery grants the client fetches directly
// from Vondel.

func (h *Handler) playbackService(w http.ResponseWriter, r *http.Request) (PlaybackService, bool) {
	if h.svc.Playback == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "playback is unavailable")
		return nil, false
	}
	return h.svc.Playback, true
}

func (h *Handler) handlePlanPlayback(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	var body PlaybackPlanRequest
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ItemID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "item_id is required")
		return
	}
	plan, err := playback.Plan(r.Context(), rc.subject, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, plan)
}

func (h *Handler) handleStartPlaybackSession(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	var body PlaybackStart
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ItemID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "item_id is required")
		return
	}
	session, err := playback.Start(r.Context(), rc.subject, rc.app.ApplicationID, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, session.Grant) {
		return
	}
	h.writeJSON(w, http.StatusCreated, session)
}

func (h *Handler) handleListPlaybackSessions(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	sessions, err := playback.Sessions(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if sessions == nil {
		sessions = []PlaybackSession{}
	}
	for _, session := range sessions {
		if !h.checkGrant(w, r, rc.app, session.Grant) {
			return
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (h *Handler) handleGetPlaybackSession(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	session, err := playback.Session(r.Context(), rc.subject, chi.URLParam(r, "sessionID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, session.Grant) {
		return
	}
	h.writeJSON(w, http.StatusOK, session)
}

func (h *Handler) handleReportPlaybackProgress(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	var body PlaybackProgress
	if !h.decodeBody(w, r, &body) {
		return
	}
	if err := playback.Progress(r.Context(), rc.subject, chi.URLParam(r, "sessionID"), body); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleStopPlaybackSession(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	playback, ok := h.playbackService(w, r)
	if !ok {
		return
	}
	if err := playback.Stop(r.Context(), rc.subject, chi.URLParam(r, "sessionID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
