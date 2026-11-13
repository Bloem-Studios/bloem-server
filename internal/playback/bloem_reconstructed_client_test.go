package playback

import (
	"context"
	"testing"
)

// A stream token's recipe carries no client metadata, so a session rebuilt
// from one asks the host for it and keeps the device it started on.
func TestReconstructSession_RestoresClientATokenCardLacks(t *testing.T) {
	m := NewTranscodeManager()
	m.Sessions = NewSessionManager(0, 0)
	var asked []string
	m.RestoreReconstructedClient = func(_ context.Context, card *RecipeCard) {
		asked = append(asked, card.SessionID)
		card.ClientName, card.ClientVersion = "Silo Android TV", "1.2.0"
		card.ClientBuild, card.ClientChannel = "31", "release"
		card.ClientUserAgent, card.DeviceID, card.DevicePlatform = "ktor-client", "device-1", "android-tv"
	}
	card := NewDirectRecipeCard("token-session", 7, "profile-1", 100)

	got := m.ReconstructSession(t.Context(), card.SessionID, card.UserID, card)
	if got == nil {
		t.Fatal("ReconstructSession refused the card")
	}
	if len(asked) != 1 || asked[0] != card.SessionID {
		t.Fatalf("restore asked for %v, want once for %q", asked, card.SessionID)
	}
	if got.ClientName != "Silo Android TV" || got.ClientVersion != "1.2.0" || got.ClientBuild != "31" ||
		got.ClientChannel != "release" || got.ClientUserAgent != "ktor-client" ||
		got.DeviceID != "device-1" || got.DevicePlatform != "android-tv" {
		t.Fatalf("rebuilt session client = %+v, want the restored device", got)
	}
}

// A stored card already names its client; the host is not asked again and
// the card's own label stands.
func TestReconstructSession_KeepsClientACardCarries(t *testing.T) {
	m := NewTranscodeManager()
	m.Sessions = NewSessionManager(0, 0)
	m.RestoreReconstructedClient = func(context.Context, *RecipeCard) {
		t.Fatal("restore asked for a card that carries its client")
	}
	card := NewDirectRecipeCard("stored-session", 7, "profile-1", 100)
	card.ClientName, card.ClientVersion, card.DevicePlatform = "Bloem Web", "2.0", "web"

	got := m.ReconstructSession(t.Context(), card.SessionID, card.UserID, card)
	if got == nil {
		t.Fatal("ReconstructSession refused the card")
	}
	if got.ClientName != "Bloem Web" || got.ClientVersion != "2.0" || got.DevicePlatform != "web" {
		t.Fatalf("rebuilt session client = %+v, want the card's", got)
	}
}
