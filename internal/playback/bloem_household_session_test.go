package playback

import (
	"context"
	"testing"
	"time"
)

// A household session reserves even without an active profile: playback
// outside the pool would let a household watch more than it pays for.
func TestHouseholdSessionReservesWithoutProfile(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2, Household: HouseholdLimits{ID: 9, MaxStreams: 3, MaxTranscodes: 1}}, nil
	})

	if _, err := manager.StartSession(7, "", 42, PlayDirect, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(store.acquires) != 1 {
		t.Fatalf("a household session must reserve, acquires = %d", len(store.acquires))
	}
	got := store.acquires[0]
	if got.HouseholdID != 9 || got.AccountID != 7 {
		t.Fatalf("request = %#v", got)
	}
}

// Moving a session between direct play and transcoding reserves against the
// same household.
func TestReplacementRequestCarriesHousehold(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2, Household: HouseholdLimits{ID: 9, MaxStreams: 3, MaxTranscodes: 1}}, nil
	})
	session, err := manager.StartSession(7, "profile", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckReplacementAllowed(context.Background(), session.ID, PlayTranscode, false); err != nil {
		t.Fatalf("replacement: %v", err)
	}
	if len(store.acquires) != 2 {
		t.Fatalf("acquires = %d, want the start and the replacement", len(store.acquires))
	}
	got := store.acquires[1]
	if !got.IsTranscode || got.HouseholdID != 9 {
		t.Fatalf("replacement request = %#v", got)
	}
}

func TestReservationValidAcceptsProfilelessHousehold(t *testing.T) {
	now := time.Now()
	request := ReservationRequest{SessionID: "s", AccountID: 7, HouseholdID: 9, LeaseUntil: now.Add(time.Minute)}
	if !request.valid(now) {
		t.Fatal("a profileless household request is invalid")
	}
	request.HouseholdID = 0
	if request.valid(now) {
		t.Fatal("a profileless request outside a household is valid")
	}
	request.HouseholdID = -1
	if request.valid(now) {
		t.Fatal("a negative household is valid")
	}
}
