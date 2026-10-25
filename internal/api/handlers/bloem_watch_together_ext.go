package handlers

import (
	"github.com/Silo-Server/silo-server/internal/watchtogether"
)

// watchTogetherRoomResponseV1 is the wire shape MarshalJSON writes. Bloem
// names it (upstream uses an anonymous struct) so the client DTO registry can
// see the response shape through the custom marshaller.
type watchTogetherRoomResponseV1 struct {
	Room            watchTogetherRoomSnapshotV1 `json:"room"`
	RoomAccessToken string                      `json:"room_access_token,omitempty"`
}

// The room websocket frames below were written as inline map literals, which
// had no nameable type for the client DTO registry
// (contracts/client/v1/registry.json). encoding/json marshals map keys in
// sorted order, so each struct declares its fields in that same order and the
// bytes on the wire are unchanged.

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
