package handlers

import (
	"encoding/json"
	"testing"

	"github.com/Silo-Server/silo-server/internal/watchtogether"
)

// TestWatchTogetherRoomFramesPinsWire pins the room-websocket frame shapes:
// the named watchTogether{Error,RoomClosed,Snapshot,Pong}Frame types replaced
// inline map literals, and this test proves the marshalled bytes did not
// change — the maps' sorted key order reproduced by the field order.
func TestWatchTogetherRoomFramesPinsWire(t *testing.T) {
	errorFrame, err := json.Marshal(watchTogetherErrorFrame{Type: "error", Code: "room_full", Message: "room is full"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"code":"room_full","message":"room is full","type":"error"}`; string(errorFrame) != want {
		t.Fatalf("error frame changed:\n got %s\nwant %s", errorFrame, want)
	}

	closedFrame, err := json.Marshal(watchTogetherRoomClosedFrame{Type: "room_closed", Reason: "host_left"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"reason":"host_left","type":"room_closed"}`; string(closedFrame) != want {
		t.Fatalf("room_closed frame changed:\n got %s\nwant %s", closedFrame, want)
	}

	snapshotFrame, err := json.Marshal(watchTogetherSnapshotFrame{
		Type:            "snapshot",
		OwnerGeneration: 3,
		Room:            watchtogether.Snapshot{RoomID: "room-1", Phase: watchtogether.RoomPhaseLobby, Generation: 2},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"owner_generation":3,"room":{"room_id":"room-1","phase":"lobby","playback_state":"","selection_mode":"","selection_revision":0,"code":"","guest_control_policy":"","is_paused":false,"anchor_position_seconds":0,"anchor_updated_at":"","generation":2,"member_count":0,"host_connected":false,"self_role":"","self_can_control_transport":false,"self_can_manage_room":false,"self_ignore_wait":false},"type":"snapshot"}`; string(snapshotFrame) != want {
		t.Fatalf("snapshot frame changed:\n got %s\nwant %s", snapshotFrame, want)
	}

	pongFrame, err := json.Marshal(watchTogetherPongFrame{
		Type:             "pong",
		OwnerGeneration:  3,
		ClientSentAt:     "2026-01-02T03:04:05.000000006Z",
		ServerReceivedAt: "2026-01-02T03:04:05.000000007Z",
		ServerSentAt:     "2026-01-02T03:04:05.000000008Z",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"client_sent_at":"2026-01-02T03:04:05.000000006Z","owner_generation":3,"server_received_at":"2026-01-02T03:04:05.000000007Z","server_sent_at":"2026-01-02T03:04:05.000000008Z","type":"pong"}`; string(pongFrame) != want {
		t.Fatalf("pong frame changed:\n got %s\nwant %s", pongFrame, want)
	}
}
