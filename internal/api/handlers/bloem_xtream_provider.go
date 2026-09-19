package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/livetv"
)

// A distinct endpoint cannot be mistaken for unauthenticated HDHomeRun setup by
// an older node during a rolling deployment or image rollback.
func (h *LiveTVHandler) HandleAddXtreamTuner(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var body livetv.AddTunerInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", "invalid JSON body")
		return
	}
	if body.Type != "" && body.Type != livetv.TunerTypeXtream {
		writeError(w, http.StatusBadRequest, "invalid_argument", "this endpoint accepts only Xtream providers")
		return
	}
	body.Type = livetv.TunerTypeXtream
	tuner, err := h.service.AddTuner(r.Context(), body)
	if errors.Is(err, livetv.ErrNotConfigured) {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "Encrypted Xtream provider storage is unavailable")
		return
	}
	if err != nil {
		writeLiveTVError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tuner)
}
