package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// TestBloemReportedDeviceKeepsBoundDevice: a direct-profile session is issued
// for one device, and a device the request reports never replaces it. The
// report still names and places a session that has no device yet.
func TestBloemReportedDeviceKeepsBoundDevice(t *testing.T) {
	h := http.Header{}
	h.Set(ClientDeviceIDHeader, "android-7f3c")
	h.Set(ClientDevicePlatformHeader, "android")
	ctx := WithClientDevice(context.Background(), h)

	bound := models.AuthSession{DeviceID: "tv-bound"}
	applyClientDevice(ctx, &bound)
	if bound.DeviceID != "tv-bound" {
		t.Fatalf("bound device = %q, want tv-bound", bound.DeviceID)
	}
	if bound.DevicePlatform != "android" {
		t.Fatalf("reported platform not recorded: %+v", bound)
	}

	open := models.AuthSession{}
	applyClientDevice(ctx, &open)
	if open.DeviceID != "android-7f3c" {
		t.Fatalf("unbound session device = %q, want the reported one", open.DeviceID)
	}
}
