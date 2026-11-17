package auth

import "github.com/Silo-Server/silo-server/internal/models"

// bloemKeepBoundDevice drops the reported device id when the session already
// names a device. A direct-profile session is bound to the device it was
// issued for, and a device the request reports never replaces that binding;
// the reported name and platform still apply.
func bloemKeepBoundDevice(session *models.AuthSession, device *ClientDevice) {
	if session.DeviceID != "" {
		device.ID = ""
	}
}
