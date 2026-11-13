package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type lifecycleClientStoreStub struct {
	PlaybackAdminStore
	asked []string
	fill  func(*playback.RecipeCard)
	err   error
}

func (s *lifecycleClientStoreStub) RestoreLifecycleClient(_ context.Context, card *playback.RecipeCard) error {
	s.asked = append(s.asked, card.SessionID)
	if s.fill != nil {
		s.fill(card)
	}
	return s.err
}

// A session rebuilt from a stream token takes back the client it started on,
// which its token never carried.
func TestReconstructedClientRestoreFillsATokenSession(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	store := &lifecycleClientStoreStub{fill: func(card *playback.RecipeCard) {
		card.ClientName, card.ClientVersion, card.DeviceID, card.DevicePlatform = "Silo Android", "1.2.0", "device-1", "android"
	}}
	h.AdminStore = store
	h.InstallReconstructedClientRestore()
	card := playback.NewDirectRecipeCard("token-session", 7, "profile", 9)

	got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card)
	if got == nil {
		t.Fatal("token recipe was not reconstructed")
	}
	if len(store.asked) != 1 || store.asked[0] != card.SessionID {
		t.Fatalf("lifecycle asked for %v, want once for %q", store.asked, card.SessionID)
	}
	if got.ClientName != "Silo Android" || got.ClientVersion != "1.2.0" || got.DeviceID != "device-1" || got.DevicePlatform != "android" {
		t.Fatalf("rebuilt session client = %+v, want the lifecycle's", got)
	}
}

// The label is best effort: a lifecycle read that fails costs the session its
// device, never its playback.
func TestReconstructedClientRestoreDoesNotBlockPlayback(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	h.AdminStore = &lifecycleClientStoreStub{err: errors.New("database unavailable")}
	h.InstallReconstructedClientRestore()
	card := playback.NewDirectRecipeCard("token-session", 7, "profile", 9)

	got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card)
	if got == nil {
		t.Fatal("a failed lifecycle read refused the reconstruct")
	}
	if got.ClientName != "" {
		t.Fatalf("client = %q, want empty", got.ClientName)
	}
}

// Without a lifecycle store the reconstruct is Silo's own.
func TestReconstructedClientRestoreWithoutLifecycleStore(t *testing.T) {
	registry := playback.NewSessionManager(0, 0)
	h := NewPlaybackHandler(registry)
	h.InstallReconstructedClientRestore()
	card := playback.NewDirectRecipeCard("token-session", 7, "profile", 9)

	if got := h.tm.ReconstructSession(t.Context(), card.SessionID, 7, card); got == nil || got.ClientName != "" {
		t.Fatalf("reconstruct without a lifecycle store = %+v", got)
	}
}
