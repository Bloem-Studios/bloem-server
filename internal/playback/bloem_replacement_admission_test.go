package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStoppedLeaseRenewalTearsDownProducer(t *testing.T) {
	store := newGenerationStore()
	store.staleAcquires = 1
	m := NewSessionManager(0, 0)
	m.SetReservationStore(store, time.Minute)
	session, err := m.StartSession(7, "p", 1, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	store.dropRow(session.ID)
	store.failNextAcquire = ErrAttemptStoppedV3
	stop, release := m.WatchTransportStop(session.ID)
	defer release()
	stops := 0
	m.SetExpirationHook(func(*Session) { t.Error("remote stop treated as a new expiry") })
	m.AddRemoteStopHook(func(s *Session) {
		stops++
		// This takes the session reservation lock and must not deadlock.
		m.CancelReplacementReservation(s.ID)
	})
	if err := m.UpdateProgress(session.ID, 1, false); !errors.Is(err, ErrAttemptStoppedV3) {
		t.Fatalf("renewal: %v", err)
	}
	select {
	case <-stop:
	default:
		t.Fatal("transport survived stopped renewal")
	}
	if stops != 1 {
		t.Fatalf("producer stop hooks = %d", stops)
	}
}

func TestReplacementAdmissionDoesNotBlockOtherSessionsOrLeakOnStop(t *testing.T) {
	store := newGenerationStore()
	m := NewSessionManager(0, 1)
	m.SetReservationStore(store, time.Minute)
	session, err := m.StartSession(7, "p", 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.StartSession(8, "q", 2, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	gate := store.armGate()
	result := make(chan error, 1)
	go func() { result <- m.CheckReplacementAllowed(context.Background(), session.ID, PlayTranscode, false) }()
	awaitSignal(t, gate.entered, "replacement acquire")
	progress := runAsync(func() {
		if err := m.UpdateProgress(other.ID, 10, false); err != nil {
			t.Error(err)
		}
	})
	awaitSignal(t, progress, "unrelated progress")
	// The pending local reservation must still count while Postgres is slow.
	if _, err := m.StartSession(7, "p", 3, PlayTranscode, false); !errors.Is(err, ErrTooManyTranscodes) {
		t.Errorf("provisional capacity was not held: %v", err)
	}
	if err := m.StopSession(session.ID); err != nil {
		t.Error(err)
	}
	close(gate.release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("replacement after stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not finish")
	}
	if _, ok := store.row(session.ID); ok {
		t.Fatal("stopped replacement leaked fleet lease")
	}
}
