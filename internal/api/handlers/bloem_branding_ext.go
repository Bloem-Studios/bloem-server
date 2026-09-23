package handlers

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/ambience"
)

// ambiencePublicSource supplies the active deployment-wide ambience packs for
// the unauthenticated branding payload (S-3).
type ambiencePublicSource interface {
	ActivePublic(ctx context.Context) ([]ambience.Wire, error)
}

// SetAmbience wires the S-3 pack registry so GET /theme/branding carries the
// active deployment-wide packs. Without it the `ambience` block is empty.
func (h *BrandingHandler) SetAmbience(src ambiencePublicSource) { h.ambience = src }
