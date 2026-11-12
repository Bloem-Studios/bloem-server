package main

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// compatNativeRecipeAcceptor accepts a native worker recipe into its grant
// (api.NativeVideoCompat.AcceptWorkerRecipe).
type compatNativeRecipeAcceptor interface {
	AcceptWorkerRecipe(context.Context, playback.RecipeCard) (playback.RecipeCard, error)
}

// bloemCompatNativeRecipeStore is the Jellyfin listener's node recipe store.
// A worker serves a native transport only from a recipe its grant accepted,
// so a native recipe is accepted before it is stored, and stored as
// accepted. Every other method is the embedded store's.
type bloemCompatNativeRecipeStore struct {
	*noderecipe.Store
	acceptor compatNativeRecipeAcceptor
	put      func(context.Context, string, playback.RecipeCard) error
}

// compatRecipeStore is what Silo's Jellyfin server needs of its node recipe
// store.
type compatRecipeStore interface {
	Put(ctx context.Context, sessionID string, card playback.RecipeCard) error
	Delete(ctx context.Context, sessionID string) error
}

// bloemCompatNativeRecipes wraps the Jellyfin listener's recipe store. A nil
// host, or a store that is not the shared node recipe store, is left as it is.
func bloemCompatNativeRecipes(store compatRecipeStore, host *api.NativeVideoCompat) compatRecipeStore {
	shared, ok := store.(*noderecipe.Store)
	if !ok || host == nil {
		return store
	}
	return newBloemCompatNativeRecipes(shared, host)
}

func newBloemCompatNativeRecipes(store *noderecipe.Store, acceptor compatNativeRecipeAcceptor) *bloemCompatNativeRecipeStore {
	return &bloemCompatNativeRecipeStore{Store: store, acceptor: acceptor, put: store.Put}
}

func (s *bloemCompatNativeRecipeStore) Put(ctx context.Context, sessionID string, card playback.RecipeCard) error {
	if s.acceptor != nil && storagesource.IsNativeLocation(card.InputPath) {
		accepted, err := s.acceptor.AcceptWorkerRecipe(ctx, card)
		if err != nil {
			return err
		}
		card = accepted
	}
	return s.put(ctx, sessionID, card)
}
