package handlers

import (
	"context"
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

// bloemValidateHello validates a realtime hello. Remote control (S-5a): a v3
// client may list names the upstream socket vocabulary does not know (replan,
// the device-rail names). The upstream validator runs over the upstream-known
// names only; the full list goes to the remote observer, which validates it
// itself. Anything unknown to both still fails here, exactly as upstream.
func bloemValidateHello(hello playback.HelloEnvelope) error {
	upstream := hello
	upstream.Capabilities.Commands = upstreamHelloCommands(hello.Capabilities.Commands)
	return upstream.Validate()
}

// bloemObserveHello mirrors a session hello into the remote control audit.
func (h *PlaybackHandler) bloemObserveHello(sessionID string, commands []playback.CommandName) {
	if h.RemoteObserver == nil {
		return
	}
	if session, err := h.sessionMgr.GetSession(sessionID); err == nil && session != nil {
		h.RemoteObserver.OnHello(context.Background(), remoteSessionInfo(session), commands)
	}
}

// bloemObserveAck mirrors a command ack into the remote control audit. Only
// the session the command was sent to may move it to accepted.
func (h *PlaybackHandler) bloemObserveAck(sessionID, commandID string) {
	if h.RemoteObserver == nil {
		return
	}
	if record, ok := h.getRealtimeCommand(commandID); ok && record.SessionID == sessionID {
		h.RemoteObserver.OnAck(context.Background(), commandID)
	}
}

// bloemObserveResult mirrors a command result into the remote control audit.
func (h *PlaybackHandler) bloemObserveResult(result playback.ResultEnvelope) {
	if h.RemoteObserver == nil {
		return
	}
	h.RemoteObserver.OnResult(context.Background(), result.CommandID, result.Status == playback.RealtimeResultStatusCompleted, result.Error)
}
