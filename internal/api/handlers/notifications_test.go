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
