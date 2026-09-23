package handlers

import (
	"encoding/json"
	"net/http"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/watchtogether"
	"github.com/go-chi/chi/v5"
)

type watchTogetherWSTicketRequest struct {
	RoomAccessToken string `json:"room_access_token"`
}

// watchTogetherRoomResponseV1 is the wire shape MarshalJSON writes. Bloem
// names it (upstream uses an anonymous struct) so the client DTO registry can
// see the response shape through the custom marshaller.
type watchTogetherRoomResponseV1 struct {
	Room            watchTogetherRoomSnapshotV1 `json:"room"`
	RoomAccessToken string                      `json:"room_access_token,omitempty"`
}

// watchTogetherErrorFrame reports a room-websocket protocol error.
type watchTogetherErrorFrame struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

// watchTogetherRoomClosedFrame tells the client the room was closed.
type watchTogetherRoomClosedFrame struct {
	Reason string `json:"reason"`
	Type   string `json:"type"`
}

// watchTogetherSnapshotFrame is the first frame on a room websocket: the full
// room snapshot plus the owner generation it was produced under.
type watchTogetherSnapshotFrame struct {
	OwnerGeneration int64                  `json:"owner_generation"`
	Room            watchtogether.Snapshot `json:"room"`
	Type            string                 `json:"type"`
}

// watchTogetherPongFrame answers a client ping with the timestamps its clock
// offset estimate needs.
type watchTogetherPongFrame struct {
	ClientSentAt     string `json:"client_sent_at"`
	OwnerGeneration  int64  `json:"owner_generation"`
	ServerReceivedAt string `json:"server_received_at"`
	ServerSentAt     string `json:"server_sent_at"`
	Type             string `json:"type"`
}

func (h *WatchTogetherHandler) validateRoomAccessTokenValue(
	rawToken string,
	roomID string,
	userID int,
	profileID string,
) error {
	if h == nil || h.TokenService == nil {
		return nil
	}

	claims, err := h.TokenService.Validate(rawToken)
	if err != nil {
		return err
	}
	if claims.RoomID != roomID || claims.UserID != userID || claims.ProfileID != profileID {
		return watchtogether.ErrRoomForbidden
	}
	return nil
}

func (h *WatchTogetherHandler) HandleMintRoomWSTicket(w http.ResponseWriter, r *http.Request) {
	claims := apimw.GetClaims(r.Context())
	profileID := apimw.GetProfileID(r.Context())
	roomID := chi.URLParam(r, "room_id")
	if claims == nil || profileID == "" || roomID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required")
		return
	}
	if h == nil || h.TokenService == nil || h.Tickets == nil {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "Watch Together websocket tickets are unavailable")
		return
	}
	var request watchTogetherWSTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid request body")
		return
	}
	if err := h.validateRoomAccessTokenValue(request.RoomAccessToken, roomID, claims.UserID, profileID); err != nil {
		writeError(w, http.StatusForbidden, "forbidden", "Room access token required")
		return
	}
	ticket, ttl, err := h.Tickets.Mint(r.Context(), auth.NewAudienceTicket(auth.AudienceWatchTogetherWS, claims, profileID, roomID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to mint websocket ticket")
		return
	}
	writeJSON(w, http.StatusOK, wsTicketResponse{Ticket: ticket, ExpiresIn: int(ttl.Seconds())})
}
