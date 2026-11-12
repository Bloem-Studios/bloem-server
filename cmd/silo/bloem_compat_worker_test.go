package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/noderecipe"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/internal/themedelivery"
)

type recipeAcceptorFixture struct {
	calls int
	err   error
}

func (a *recipeAcceptorFixture) AcceptWorkerRecipe(_ context.Context, card playback.RecipeCard) (playback.RecipeCard, error) {
	a.calls++
	card.RoutingExecutionNodeID = 7
	return card, a.err
}

// The Jellyfin listener's stored recipe for a native worker transport is
// accepted into the worker grant first, and stored as accepted. Other
// recipes are stored unchanged; a refused one is not stored.
func TestCompatNativeRecipesAcceptNativeRecipesBeforeStoring(t *testing.T) {
	location, err := storagesource.CatalogLocation(uuid.New(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	acceptor := &recipeAcceptorFixture{}
	var stored []playback.RecipeCard
	store := newBloemCompatNativeRecipes(noderecipe.NewStore(nil, 0), acceptor)
	store.put = func(_ context.Context, _ string, card playback.RecipeCard) error {
		stored = append(stored, card)
		return nil
	}
	if err := store.Put(t.Context(), "session", playback.RecipeCard{SessionID: "session", InputPath: "/media/movie.mkv"}); err != nil {
		t.Fatal(err)
	}
	if acceptor.calls != 0 || len(stored) != 1 || stored[0].RoutingExecutionNodeID != 0 {
		t.Fatalf("ordinary recipe: calls=%d stored=%+v", acceptor.calls, stored)
	}
	if err := store.Put(t.Context(), "session", playback.RecipeCard{SessionID: "session", InputPath: location}); err != nil {
		t.Fatal(err)
	}
	if acceptor.calls != 1 || len(stored) != 2 || stored[1].RoutingExecutionNodeID != 7 {
		t.Fatalf("native recipe: calls=%d stored=%+v", acceptor.calls, stored)
	}
	acceptor.err = errors.New("no grant for this worker")
	if err := store.Put(t.Context(), "session", playback.RecipeCard{SessionID: "session", InputPath: location}); err == nil || len(stored) != 2 {
		t.Fatalf("refused recipe: err=%v stored=%d", err, len(stored))
	}
}

// The wrapped store keeps the theme-delivery capability Silo's Jellyfin
// server asserts on its recipe store.
func TestCompatNativeRecipesKeepThemeDeliveryStore(t *testing.T) {
	wrapped := bloemCompatNativeRecipes(noderecipe.NewStore(nil, 0), api.NewNativeVideoCompat())
	if _, ok := wrapped.(*bloemCompatNativeRecipeStore); !ok {
		t.Fatalf("shared store not wrapped: %T", wrapped)
	}
	if _, ok := wrapped.(themedelivery.RecipeStore); !ok {
		t.Fatal("wrapped store lost the theme-delivery recipe store")
	}
	if unwrapped := bloemCompatNativeRecipes(noderecipe.NewStore(nil, 0), nil); unwrapped == wrapped {
		t.Fatal("unexpected shared wrapper")
	} else if _, ok := unwrapped.(*noderecipe.Store); !ok {
		t.Fatalf("store without a host was wrapped: %T", unwrapped)
	}
}

// The approved S6 seam seals the worker grant in Silo's Jellyfin remote start
// before the request leaves; main.go routes the listener's recipes through
// the acceptor.
func TestCompatNativeWorkerStartIsWired(t *testing.T) {
	source, err := os.ReadFile("../../internal/jellycompat/handlers_playback.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	seal := strings.Index(text, "playback.SealContextWorkerStart(requestCtx, transcodeNodeURL, body)")
	send := strings.Index(text, `telemetry.DoTrustedNode(transcodeproxy.NodeClient(), httpReq, "transcode_start")`)
	if seal < 0 || send < 0 || seal > send {
		t.Fatalf("remote start must seal the worker grant before it is sent (seal=%d send=%d)", seal, send)
	}
	main, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "compatDeps.RecipeNodeStore = bloemCompatNativeRecipes(compatDeps.RecipeNodeStore, deps.NativeVideoCompat)") {
		t.Fatal("main.go does not route Jellyfin recipes through the native recipe acceptor")
	}
}
