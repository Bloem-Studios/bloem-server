package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/preview"
)

func (h *BloemPreviewHandler) SetEvents(events *preview.Events) {
	h.events = events
	h.eventLimit = preview.NewEventLimiter()
}
func (h *BloemPreviewHandler) HandleEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := apimw.GetClaims(r.Context())
	if !apimw.IsPreviewPrincipal(r.Context()) || claims == nil || claims.SessionID == "" {
		apimw.WritePreviewOnly(w)
		return
	}
	var body struct {
		Event string `json:"event"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || !preview.ValidEvent(body.Event) {
		writeError(w, 400, "invalid_request", "Invalid preview event.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, 400, "invalid_request", "One JSON document is required.")
		return
	}
	if h.events == nil || h.eventLimit == nil {
		writeError(w, 503, "preview_unavailable", "Preview counters are unavailable.")
		return
	}
	if !h.eventLimit.Allow(claims.SessionID) {
		w.Header().Set("Retry-After", "10")
		writeError(w, 429, "rate_limited", "Too many preview events.")
		return
	}
	if err := h.events.Increment(r.Context(), body.Event); err != nil {
		writeError(w, 503, "preview_unavailable", "Preview counters are unavailable.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
