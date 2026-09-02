package handlers

import (
	"encoding/json"
	"testing"
)

// TestNotificationDismissedPayloadPinsWire pins the dismissed event payload:
// the named notificationDismissedPayload replaced an inline map[string]any
// literal, and this test proves the marshalled bytes did not change — the
// map's sorted key order ("id" before "profile_id") reproduced by the field
// order.
func TestNotificationDismissedPayloadPinsWire(t *testing.T) {
	got, err := json.Marshal(notificationDismissedPayload{ID: "n-1", ProfileID: "p-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"id":"n-1","profile_id":"p-1"}`
	if string(got) != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestNotificationReadPayloadPinsWire pins the read event payload: the named
// notificationReadPayload replaced a conditionally-built inline
// map[string]any literal, and this test proves the marshalled bytes did not
// change for both shapes — single notification ({"id",…}) and read-all
// ({"all":true,…}) — with the map's sorted key order reproduced by the field
// order and omitempty.
func TestNotificationReadPayloadPinsWire(t *testing.T) {
	single, err := json.Marshal(notificationReadPayload{ID: "n-1", ProfileID: "p-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"id":"n-1","profile_id":"p-1"}`; string(single) != want {
		t.Fatalf("single wire changed:\n got %s\nwant %s", single, want)
	}

	all, err := json.Marshal(notificationReadPayload{All: true, ProfileID: "p-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"all":true,"profile_id":"p-1"}`; string(all) != want {
		t.Fatalf("read-all wire changed:\n got %s\nwant %s", all, want)
	}
}
