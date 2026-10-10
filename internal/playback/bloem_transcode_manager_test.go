package playback

// Bloem authority and strict reconstruct admission coverage.
// Silo's transcode_manager_test.go keeps the default fail-open cases.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestLoadOrReconstructSession_BloemAuthorityDenialsWithoutStrictAdmission(t *testing.T) {
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
			manager.StrictAdmissionFn = func() bool { return false }
			card := RecipeCard{
				SessionID: "denied-recipe", UserID: 7, ProfileID: "recipe-profile",
				MediaFileID: 100, PlayMethod: tc.method,
			}

			got, status := manager.LoadOrReconstructSession(t.Context(), registry.GetSession, card.SessionID, card.UserID, &card)
			if status != SessionMissing || got != nil {
				t.Fatalf("authority-denied recipe reconstructed with strict mode off: status=%v session=%+v", status, got)
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

func TestReconstructSession_WrappedAuthorityDenialsWithoutStrictAdmission(t *testing.T) {
	for _, denial := range []error{ErrPlaybackNotAllowed, ErrTenantFrozen, ErrTenantTranscodesExceeded} {
		t.Run(denial.Error(), func(t *testing.T) {
			registry := NewSessionManager(0, 0)
			registry.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
				return SessionLimits{}, fmt.Errorf("current admission authority: %w", denial)
			})
			manager := NewTranscodeManager()
			manager.Sessions = registry
			manager.StrictAdmissionFn = func() bool { return false }
			card := NewDirectRecipeCard("denied-recipe", 7, "recipe-profile", 100)

			// limitsForUser wraps provider errors with ErrLimitProviderUnavailable.
			// A nested authority denial must still take precedence over availability.
			if got := manager.ReconstructSession(t.Context(), card.SessionID, card.UserID, card); got != nil {
				t.Fatalf("wrapped authority denial reconstructed with strict mode off: %+v", got)
			}
			if _, err := registry.GetSession(card.SessionID); !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("denied recipe registered: %v", err)
			}
		})
	}
}

func TestLoadOrReconstructSession_LimitAvailabilityUsesStrictAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		strict func() bool
		want   SessionLoadStatus
	}{
		{name: "upstream default", want: SessionLoaded},
		{name: "strict disabled", strict: func() bool { return false }, want: SessionLoaded},
		{name: "strict enabled", strict: func() bool { return true }, want: SessionMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewSessionManager(0, 0)
			registry.SetLimitProvider(func(context.Context, int) (SessionLimits, error) {
				return SessionLimits{}, errors.New("database unavailable")
			})
			manager := NewTranscodeManager()
			manager.Sessions = registry
			manager.StrictAdmissionFn = tc.strict
			card := NewDirectRecipeCard("recoverable-recipe", 7, "", 100)

			got, status := manager.LoadOrReconstructSession(t.Context(), registry.GetSession, card.SessionID, card.UserID, &card)
			if status != tc.want {
				t.Fatalf("unavailable limit provider status=%v, want %v", status, tc.want)
			}
			if tc.want == SessionLoaded {
				if got == nil || got.ID != card.SessionID || got.UserID != 7 {
					t.Fatalf("unavailable limit provider lost recovered session: %+v", got)
				}
				if registered, err := registry.GetSession(card.SessionID); err != nil || registered.UserID != 7 || registered.MediaFileID != 100 {
					t.Fatalf("recovered session not registered: session=%+v error=%v", registered, err)
				}
			} else if _, err := registry.GetSession(card.SessionID); got != nil || !errors.Is(err, ErrSessionNotFound) {
				t.Fatalf("strict admission registered unevaluated recipe: session=%+v error=%v", got, err)
			}
		})
	}
}

// With playback.strict_reconstruct_admission enabled, the same unevaluated-limit
// case must refuse instead. This is Bloem's deliberate divergence from upstream,
// expressed as an operator setting rather than a fork of the admission path.
func TestReconstructSession_ProviderErrorFailsClosedUnderStrictAdmission(t *testing.T) {
	ctx := context.Background()
	reg := &fakeSessionRegistry{
		limitsErr: fmt.Errorf("load session limits for user 7: %w",
			errors.Join(ErrLimitProviderUnavailable, errors.New("db timeout"))),
	}
	m := NewTranscodeManager()
	m.Sessions = reg
	m.StrictAdmissionFn = func() bool { return true }

	card := NewDirectRecipeCard("a", 7, "p", 100)
	got := m.ReconstructSession(ctx, "a", 7, card)
	if got != nil {
		t.Fatalf("strict admission must refuse an unevaluated limit provider, got session %+v", got)
	}
	if _, err := reg.GetSession("a"); err == nil {
		t.Fatal("refused session was unexpectedly registered")
	}
}

// ...and must yield SessionMissing under the strict posture.
func TestLoadOrReconstructSession_ProviderErrorFailsClosedUnderStrictAdmission(t *testing.T) {
	ctx := context.Background()
	reg := &fakeSessionRegistry{
		limitsErr: errors.Join(ErrLimitProviderUnavailable, errors.New("db timeout")),
	}
	m := NewTranscodeManager()
	m.Sessions = reg
	m.StrictAdmissionFn = func() bool { return true }

	card := NewDirectRecipeCard("s", 5, "p", 77)
	got, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 5, &card)
	if status != SessionMissing || got != nil {
		t.Fatalf("strict admission must yield SessionMissing, got status=%v session=%+v", status, got)
	}
}
