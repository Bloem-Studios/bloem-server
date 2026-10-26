package playback

// Bloem authority denials on reconstruct. Silo's transcode_manager_test.go
// keeps the fail-open cases for an unevaluated limit provider.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestLoadOrReconstructSession_BloemAuthorityDenials(t *testing.T) {
	for _, tc := range []struct {
		name              string
		limits            SessionLimits
		method            PlayMethod
		occupiedTranscode bool
	}{
		{name: "profile direct", limits: SessionLimits{PlaybackDisabled: true}, method: PlayDirect},
		{name: "profile remux", limits: SessionLimits{PlaybackDisabled: true}, method: PlayRemux},
		{name: "profile transcode", limits: SessionLimits{PlaybackDisabled: true}, method: PlayTranscode},
		{name: "frozen tenant", limits: SessionLimits{TenantID: "tenant-a", TenantFrozen: true}, method: PlayDirect},
		{name: "tenant transcode cap", limits: SessionLimits{TenantID: "tenant-a", TenantMaxTranscodes: 1}, method: PlayTranscode, occupiedTranscode: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewSessionManager(0, 0)
			registry.SetProfileLimitProvider(func(_ context.Context, _ int, profileID string) (SessionLimits, error) {
				if profileID != "recipe-profile" && profileID != "other-profile" {
					t.Fatalf("unexpected admission profile %q", profileID)
				}
				return tc.limits, nil
			})
			var existing *Session
			if tc.occupiedTranscode {
				var err error
				existing, err = registry.StartSession(8, "other-profile", 101, PlayTranscode, false)
				if err != nil {
					t.Fatalf("occupy tenant transcode slot: %v", err)
				}
			}
			manager := NewTranscodeManager()
			manager.Sessions = registry
			card := RecipeCard{
				SessionID: "denied-recipe", UserID: 7, ProfileID: "recipe-profile",
				MediaFileID: 100, PlayMethod: tc.method,
			}

			got, status := manager.LoadOrReconstructSession(t.Context(), registry.GetSession, card.SessionID, card.UserID, &card)
			if status != SessionMissing || got != nil {
				t.Fatalf("authority-denied recipe reconstructed: status=%v session=%+v", status, got)
			}
			if _, err := registry.GetSession(card.SessionID); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("denied recipe registered: %v", err)
			}
			if existing != nil {
				if got, err := registry.GetSession(existing.ID); err != nil || got.UserID != 8 || got.PlayMethod != PlayTranscode || got.TenantID != "tenant-a" {
					t.Fatalf("existing tenant transcode changed: session=%+v error=%v", got, err)
				}
			}
		})
	}
}

func TestReconstructSession_WrappedAuthorityDenials(t *testing.T) {
	for _, denial := range []error{ErrPlaybackNotAllowed, ErrTenantFrozen, ErrTenantTranscodesExceeded} {
		t.Run(denial.Error(), func(t *testing.T) {
			registry := NewSessionManager(0, 0)
			registry.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
				return SessionLimits{}, fmt.Errorf("current admission authority: %w", denial)
			})
			manager := NewTranscodeManager()
			manager.Sessions = registry
			card := NewDirectRecipeCard("denied-recipe", 7, "recipe-profile", 100)

			// limitsForUser wraps provider errors with ErrLimitProviderUnavailable.
			// A nested authority denial must still take precedence over availability.
			if got := manager.ReconstructSession(t.Context(), card.SessionID, card.UserID, card); got != nil {
				t.Fatalf("wrapped authority denial reconstructed: %+v", got)
			}
			if _, err := registry.GetSession(card.SessionID); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("denied recipe registered: %v", err)
			}
		})
	}
}
