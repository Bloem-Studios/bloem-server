package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// TestRestoreLifecycleClientDB pins where a rebuilt session's client comes
// from: the facts its lifecycle row recorded at the start, for the session's
// own account only, filling only what the card lacks.
func TestRestoreLifecycleClientDB(t *testing.T) {
	ctx, pool, account := lifecycleTestDB(t)
	store := NewPGPlaybackAdminStore(pool, nil)
	session := uuid.NewString()
	facts, err := json.Marshal(map[string]any{
		"client_name": "Silo Android TV", "client_version": "1.2.0", "client_build": "31",
		"client_channel": "release", "client_user_agent": "ktor-client",
		"device_id": "device-1", "device_platform": "android-tv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.StartLifecycle(ctx, LifecycleStart{
		SessionID: session, Frontend: LifecycleFrontendNative, UserID: account, ProfileID: "p-1",
		MediaFileID: 7, RequestedMediaFileID: 7, StartedAt: time.Now().UTC(), LiveRow: facts,
	}); err != nil || !ok {
		t.Fatalf("start lifecycle: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(t.Context(), `DELETE FROM playback_session_lifecycle WHERE session_id = $1::uuid`, session)
	})

	card := playback.NewDirectRecipeCard(session, account, "p-1", 7)
	if err := store.RestoreLifecycleClient(ctx, &card); err != nil {
		t.Fatal(err)
	}
	if card.ClientName != "Silo Android TV" || card.ClientVersion != "1.2.0" || card.ClientBuild != "31" ||
		card.ClientChannel != "release" || card.ClientUserAgent != "ktor-client" ||
		card.DeviceID != "device-1" || card.DevicePlatform != "android-tv" {
		t.Fatalf("restored card client = %+v", card)
	}

	partial := playback.NewDirectRecipeCard(session, account, "p-1", 7)
	partial.DevicePlatform = "tv"
	if err := store.RestoreLifecycleClient(ctx, &partial); err != nil {
		t.Fatal(err)
	}
	if partial.DevicePlatform != "tv" || partial.ClientName != "Silo Android TV" {
		t.Fatalf("partial card = %+v, want its own platform kept and the rest filled", partial)
	}

	for name, other := range map[string]playback.RecipeCard{
		"another account": playback.NewDirectRecipeCard(session, account+1, "p-1", 7),
		"unknown session": playback.NewDirectRecipeCard(uuid.NewString(), account, "p-1", 7),
		"non-uuid id":     playback.NewDirectRecipeCard("not-a-uuid", account, "p-1", 7),
	} {
		if err := store.RestoreLifecycleClient(ctx, &other); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if other.ClientName != "" || other.DeviceID != "" {
			t.Fatalf("%s: card took a client it does not own: %+v", name, other)
		}
	}
}
