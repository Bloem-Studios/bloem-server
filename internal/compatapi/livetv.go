package compatapi

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Live TV operations expose Prairie-derived channels, guide, tuner
// availability, stream authorization, DVR rules, and recordings. Tuner and
// provider credentials never cross this boundary; streams resolve to
// audience-bound delivery grants.

func (h *Handler) livetvService(w http.ResponseWriter, r *http.Request) (LiveTVService, bool) {
	if h.svc.LiveTV == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "live tv is unavailable")
		return nil, false
	}
	return h.svc.LiveTV, true
}

func (h *Handler) handleListLiveTvChannels(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listLiveTvChannels"})
	if !ok {
		return
	}
	page, err := livetv.Channels(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if page.Channels == nil {
		page.Channels = []Channel{}
	}
	h.writeJSON(w, http.StatusOK, h.listPayload("listLiveTvChannels", "channels", page.Channels, page.NextCursor))
}

func (h *Handler) handleGetLiveTvGuide(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "getLiveTvGuide"})
	if !ok {
		return
	}
	query := r.URL.Query()
	guideQuery := GuideQuery{ChannelID: query.Get("channel_id"), Cursor: cursor, Limit: limit}
	for _, window := range []struct {
		name string
		into *time.Time
	}{{"start", &guideQuery.Start}, {"end", &guideQuery.End}} {
		raw := query.Get(window.name)
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "invalid_request", window.name+" must be an RFC 3339 timestamp")
			return
		}
		*window.into = parsed
	}
	page, err := livetv.Guide(r.Context(), rc.subject, guideQuery)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if page.Entries == nil {
		page.Entries = []GuideEntry{}
	}
	h.writeJSON(w, http.StatusOK, h.listPayload("getLiveTvGuide", "entries", page.Entries, page.NextCursor))
}

func (h *Handler) handleListLiveTvTuners(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	tuners, err := livetv.Tuners(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if tuners == nil {
		tuners = []Tuner{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"tuners": tuners})
}

func (h *Handler) handleAuthorizeLiveTvStream(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.ChannelID == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "channel_id is required")
		return
	}
	grant, err := livetv.AuthorizeStream(r.Context(), rc.subject, rc.app.ApplicationID, body.ChannelID)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, &grant) {
		return
	}
	h.writeJSON(w, http.StatusCreated, grant)
}

func (h *Handler) handleListDvrRules(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	rules, err := livetv.DVRRules(r.Context(), rc.subject)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if rules == nil {
		rules = []DVRRule{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *Handler) handleCreateDvrRule(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	var body DVRRuleCreate
	if !h.decodeBody(w, r, &body) {
		return
	}
	if body.Kind == "" {
		h.writeError(w, r, http.StatusBadRequest, "invalid_request", "kind is required")
		return
	}
	rule, err := livetv.CreateDVRRule(r.Context(), rc.subject, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, rule)
}

func (h *Handler) handleDeleteDvrRule(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	if err := livetv.DeleteDVRRule(r.Context(), rc.subject, chi.URLParam(r, "ruleID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListRecordings(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listRecordings"})
	if !ok {
		return
	}
	page, err := livetv.Recordings(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if page.Recordings == nil {
		page.Recordings = []Recording{}
	}
	for _, recording := range page.Recordings {
		if !h.checkGrant(w, r, rc.app, recording.Grant) {
			return
		}
	}
	h.writeJSON(w, http.StatusOK, h.listPayload("listRecordings", "recordings", page.Recordings, page.NextCursor))
}

func (h *Handler) handleGetRecording(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	recording, err := livetv.Recording(r.Context(), rc.subject, chi.URLParam(r, "recordingID"))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !h.checkGrant(w, r, rc.app, recording.Grant) {
		return
	}
	h.writeJSON(w, http.StatusOK, recording)
}

func (h *Handler) handleDeleteRecording(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	livetv, ok := h.livetvService(w, r)
	if !ok {
		return
	}
	if err := livetv.DeleteRecording(r.Context(), rc.subject, chi.URLParam(r, "recordingID")); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
