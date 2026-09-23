package handlers

import (
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

func (h *EventsHandler) SetAudienceTicketStore(store auth.AudienceTicketStore) {
	if h != nil {
		h.audienceTickets = store
	}
}

// HandleMintWSTicket mints the short-lived, single-use credential used only
// for the events websocket handshake.
func (h *EventsHandler) HandleMintWSTicket(w http.ResponseWriter, r *http.Request) {
	claims := apimw.GetClaims(r.Context())
	if claims == nil || h == nil || h.audienceTickets == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Websocket tickets are unavailable")
		return
	}
	ticket, ttl, err := h.audienceTickets.Mint(r.Context(), auth.NewAudienceTicket(auth.AudienceEventsWS, claims, apimw.GetProfileID(r.Context()), ""))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mint websocket ticket")
		return
	}
	writeJSON(w, http.StatusOK, wsTicketResponse{Ticket: ticket, ExpiresIn: int(ttl.Seconds())})
}
