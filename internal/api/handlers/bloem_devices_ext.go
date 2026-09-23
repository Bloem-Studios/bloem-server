package handlers

import (
	"context"
	"log/slog"
	"net/http"
)

// remoteCapabilityForgetter is the remote control service's forget hook.
type remoteCapabilityForgetter interface {
	ForgetDevice(ctx context.Context, userID int, profileID, deviceID string) error
}

// bloemForgetRemoteCapabilities drops a forgotten device's remote_control
// block. A forgotten device is not controllable; the registry row is already
// gone, so a failure here is logged rather than turned into a failed forget
// (the advertisement route re-checks the registry anyway).
func (h *DeviceHandler) bloemForgetRemoteCapabilities(r *http.Request, forget bool, userID int, profileID, deviceID string) {
	if !forget || h.RemoteCapabilities == nil {
		return
	}
	if err := h.RemoteCapabilities.ForgetDevice(r.Context(), userID, profileID, deviceID); err != nil {
		slog.WarnContext(r.Context(), "remote control block not dropped on forget", "component", "api", "device_id", deviceID, "error", err)
	}
}
