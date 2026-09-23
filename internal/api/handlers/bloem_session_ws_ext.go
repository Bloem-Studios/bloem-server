package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/remote"
)

// HandleMintSessionWSTicket mints a single-use ticket bound to the exact
// playback session after applying the same ownership check as the socket.
func (h *PlaybackHandler) HandleMintSessionWSTicket(w http.ResponseWriter, r *http.Request) {
	claims := apimw.GetClaims(r.Context())
	sessionID := chi.URLParam(r, "session_id")
	if h == nil || h.sessionMgr == nil || h.AudienceTickets == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Playback websocket tickets are unavailable")
		return
	}
	if claims == nil || claims.UserID == 0 || sessionID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	session, err := h.sessionMgr.GetSession(sessionID)
	if err != nil {
		writePlaybackSessionNotFound(w)
		return
	}
	if !callerOwnsPlaybackSession(r, session.UserID, session.ProfileID, claims.UserID) {
		writeError(w, http.StatusForbidden, "forbidden", "Playback session access denied")
		return
	}
	// The ticket names the profile this request verified, not the session's:
	// the control route needs no profile, and the handshake skips PIN
	// verification on the strength of this request's check. Carrying
	// session.ProfileID let an account caller act as a PIN-protected profile
	// it never unlocked.
	ticket, ttl, err := h.AudienceTickets.Mint(r.Context(), auth.NewAudienceTicket(auth.AudiencePlaybackControlWS, claims, apimw.GetProfileID(r.Context()), sessionID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mint websocket ticket")
		return
	}
	writeJSON(w, http.StatusOK, wsTicketResponse{Ticket: ticket, ExpiresIn: int(ttl.Seconds())})
}

// upstreamHelloCommands drops the remote-control-only names from a hello's
// command list so the upstream validator sees only its own vocabulary.
func upstreamHelloCommands(commands []playback.CommandName) []playback.CommandName {
	kept := make([]playback.CommandName, 0, len(commands))
	for _, name := range commands {
		if remote.IsRemoteOnlyCommand(name) {
			continue
		}
		kept = append(kept, name)
	}
	return kept
}
