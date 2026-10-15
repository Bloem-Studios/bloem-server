package apiv2

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBloemDevicePairingDoesNotAdvertiseRetiredProtocol(t *testing.T) {
	rec := do(t, newTestHandler(t, pilotDeps(nil, nil)), http.MethodGet, "/api/v2/auth/device/capability", "", nil)
	var body DeviceLoginCapability
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.JellyfinQuickConnect {
		t.Fatal("removed Jellyfin sign-in is advertised")
	}
	if !body.RemotePlaybackHandoff {
		t.Fatal("native playback handoff was removed")
	}
}
