package main

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type initialLegacyGrantReader interface {
	Get(context.Context, string) (*playback.RecipeCard, bool)
}

type initialNodeGrantLookup struct {
	runtime interface {
		ResolveCurrentSession(context.Context, string) (*playback.RecipeCard, bool, error)
	}
	legacy initialLegacyGrantReader
}

func (r initialNodeGrantLookup) Get(ctx context.Context, sessionID string) (*playback.RecipeCard, bool) {
	card, bound, err := r.runtime.ResolveCurrentSession(ctx, sessionID)
	if bound || err != nil {
		return card, err == nil && card != nil
	}
	return r.legacy.Get(ctx, sessionID)
}
