package handlers

import (
	"context"
)

// remoteCapabilityForgetter is the remote control service's forget hook.
type remoteCapabilityForgetter interface {
	ForgetDevice(ctx context.Context, userID int, profileID, deviceID string) error
}
