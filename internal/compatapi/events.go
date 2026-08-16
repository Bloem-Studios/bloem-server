package compatapi

import "net/http"

// Events serves subject-filtered events with a resumable signed cursor.
// When the domain reports a gap it sets Resync and supplies a fresh cursor;
// the companion performs a bounded resynchronization instead of trusting its
// own cache.

func (h *Handler) handleListEvents(w http.ResponseWriter, r *http.Request, rc *requestContext) {
	if h.svc.Events == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "unavailable", "events are unavailable")
		return
	}
	cursor, limit, ok := h.pageParams(w, r, Operation{ID: "listEvents"})
	if !ok {
		return
	}
	batch, err := h.svc.Events.Events(r.Context(), rc.subject, cursor, limit)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if batch.Events == nil {
		batch.Events = []Event{}
	}
	payload := h.listPayload("listEvents", "events", batch.Events, batch.NextCursor)
	payload["resync"] = batch.Resync
	h.writeJSON(w, http.StatusOK, payload)
}
