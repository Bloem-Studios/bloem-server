package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// Preserve the originating admission callback, while cancellation and deadline
// remain the existing application/worker lifecycle. No-origin calls stay trusted.
func nativeMutationContinuation(origin, lifecycle context.Context) context.Context {
	return catalog.CarryNativePhaseOrigin(origin, lifecycle)
}
