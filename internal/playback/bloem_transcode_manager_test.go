package playback

// Bloem authority denials on reconstruct. Silo's transcode_manager_test.go
// keeps the fail-open cases for an unevaluated limit provider.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
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

// householdReconstructRegistry is a session manager whose reservation store
// fails, or refuses, as a test sets it, with limits that name household 9
// for a household test and no household otherwise.
func householdReconstructRegistry(t *testing.T, store *recordingReservationStore, household int, limitsErr func() error) (*SessionManager, *TranscodeManager, *atomic.Int32) {
	t.Helper()
	registry := NewSessionManager(0, 0)
	registry.SetReservationStore(store, time.Minute)
	var lookups atomic.Int32
	registry.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		lookups.Add(1)
		if limitsErr != nil {
			if err := limitsErr(); err != nil {
				return SessionLimits{}, err
			}
		}
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2, Household: HouseholdLimits{ID: household}}, nil
	})
	manager := NewTranscodeManager()
	manager.Sessions = registry
	return registry, manager, &lookups
}

// A session rebuilt after a restart whose reservation call fails for a reason
// other than a limit (the store's timeout, a database blip) is refused when its
// account plays in a pooled household: admitted unreserved, nothing would ever
// count it against the pool. Any other account keeps Silo's fail-open rebuild.
func TestReconstructWithoutItsReservationFailsClosedOnlyInAHousehold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		household int
		admitted  bool
	}{
		{name: "household login", household: 9, admitted: false},
		{name: "account in no household", household: 0, admitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingReservationStore{acquireErr: context.DeadlineExceeded}
			registry, manager, _ := householdReconstructRegistry(t, store, tc.household, nil)
			card := NewDirectRecipeCard("rebuilt-recipe", 7, "recipe-profile", 100)

			got := manager.ReconstructSession(t.Context(), card.SessionID, card.UserID, card)
			if admitted := got != nil; admitted != tc.admitted {
				t.Fatalf("rebuilt session admitted=%t, want %t", admitted, tc.admitted)
			}
			if _, err := registry.GetSession(card.SessionID); (err == nil) != tc.admitted {
				t.Fatalf("registered session: %v, want admitted=%t", err, tc.admitted)
			}
		})
	}
}

// A session rebuilt while its limits could not be read at all is admitted, as
// Silo does, because its household is unknown. The next cleanup pass that can
// read its limits reserves it when it plays in a pooled household, and ends it
// when the pool has no room for it; an account in no household is left as
// Silo leaves it.
func TestRebuiltSessionWithUnreadableLimitsReservesOnceTheyCanBeRead(t *testing.T) {
	for _, tc := range []struct {
		name        string
		household   int
		refuse      error
		wantLive    bool
		wantAcquire bool
	}{
		{name: "household with room", household: 9, wantLive: true, wantAcquire: true},
		{name: "full household", household: 9, refuse: ErrHouseholdStreamsExceeded, wantLive: false, wantAcquire: true},
		{name: "account in no household", household: 0, wantLive: true, wantAcquire: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &recordingReservationStore{}
			var unreadable atomic.Bool
			unreadable.Store(true)
			registry, manager, lookups := householdReconstructRegistry(t, store, tc.household, func() error {
				if unreadable.Load() {
					return errors.New("db timeout")
				}
				return nil
			})
			card := NewDirectRecipeCard("unknown-household-recipe", 7, "recipe-profile", 100)
			if got := manager.ReconstructSession(t.Context(), card.SessionID, card.UserID, card); got == nil {
				t.Fatal("a rebuild with unreadable limits must fail open")
			}

			// Still unreadable: the pass tries again later and changes nothing.
			registry.CleanStale()
			if _, err := registry.GetSession(card.SessionID); err != nil {
				t.Fatalf("the rebuilt session ended while its limits were unreadable: %v", err)
			}
			unreadable.Store(false)
			store.mu.Lock()
			store.acquireErr = tc.refuse
			store.mu.Unlock()
			registry.CleanStale()

			store.mu.Lock()
			acquires := append([]ReservationRequest(nil), store.acquires...)
			store.mu.Unlock()
			if acquired := len(acquires) == 1 && acquires[0].SessionID == card.SessionID && acquires[0].HouseholdID == tc.household; acquired != tc.wantAcquire {
				t.Fatalf("acquires = %+v, want a reservation=%t", acquires, tc.wantAcquire)
			}
			got, err := registry.GetSession(card.SessionID)
			if (err == nil) != tc.wantLive {
				t.Fatalf("rebuilt session live=%t (%v), want %t", err == nil, err, tc.wantLive)
			}
			if tc.wantLive && tc.wantAcquire && got.reservationGeneration <= 0 {
				t.Fatalf("the healed session holds no reservation: %+v", got)
			}

			// Settled: later passes neither look the limits up again nor reserve.
			before := lookups.Load()
			registry.CleanStale()
			if lookups.Load() != before {
				t.Fatal("a settled session's limits were looked up again")
			}
		})
	}
}
