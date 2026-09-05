package handlers

import (
	"net/http"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/livetv"
)

// HandleCapability never returns channel metadata to viewers without a grant.
// Database failures remain errors, not a misleading empty/disabled capability.
func (h *LiveTVHandler) HandleCapability(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scope, ok := access.GetScope(r.Context())
	response := livetv.CapabilityResponse{Supported: true, Allowed: ok && scope.LiveTVAllowed, HeartbeatIntervalSeconds: 30}
	if response.Allowed {
		channels, err := h.service.ListChannels(r.Context(), "")
		if err != nil {
			writeLiveTVError(w, err)
			return
		}
		for _, channel := range channels {
			if channel.Enabled {
				response.Available = true
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}
