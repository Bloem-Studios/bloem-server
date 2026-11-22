package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

// quietPausedSession makes a session look like a killed app's: paused, with no
// client traffic for ten minutes. Its encoder (and its transcode slot here)
// is kept for DefaultPausedSessionGrace.
func quietPausedSession(manager *SessionManager, sessionID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	s := manager.sessions[sessionID]
	s.IsPaused = true
	s.LastActivityAt = time.Now().Add(-10 * time.Minute)
	s.UpdatedAt = s.LastActivityAt
	s.PlanCommittedAt = s.LastActivityAt
}

// A quiet paused transcode keeps its encoder until cleanup reaps it, so the
// cleanup pass keeps its fleet reservation current; client traffic alone
// would let it lapse after the lease. A direct play's stream slot follows
// client traffic, as it does here (PausedStreamSlotGrace), and is left alone.
func TestCleanupRenewsTheReservationOfAHeldTranscode(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2}, nil
	})
	transcode, err := manager.StartSession(7, "profile", 42, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := manager.StartSession(7, "profile", 43, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	quietPausedSession(manager, transcode.ID)
	quietPausedSession(manager, direct.ID)

	if expired := manager.CleanStale(); len(expired) != 0 {
		t.Fatalf("cleanup reaped %d sessions inside their paused grace", len(expired))
	}
	store.mu.Lock()
	renews := append([]Reservation(nil), store.renews...)
	store.mu.Unlock()
	if len(renews) != 1 || renews[0].SessionID != transcode.ID {
		t.Fatalf("renewals = %+v, want one, for the held transcode", renews)
	}
	if _, err := manager.GetSession(transcode.ID); err != nil {
		t.Fatalf("the held transcode: %v", err)
	}
}

// A held transcode whose lease lapsed anyway (the store was unreachable for
// longer than the lease) and whose slot the store no longer gives back is
// ended as cleanup ends an idle session, so its encoder stops.
func TestCleanupEndsAHeldTranscodeThatLostItsReservation(t *testing.T) {
	store := &leaseLossReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetProfileLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 2}, nil
	})
	var ended []string
	manager.AddExpirationHook(func(s *Session) { ended = append(ended, s.ID) })
	transcode, err := manager.StartSession(7, "profile", 42, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	quietPausedSession(manager, transcode.ID)

	manager.CleanStale()
	if _, err := manager.GetSession(transcode.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("a transcode that lost its reservation is still live: %v", err)
	}
	if len(ended) != 1 || ended[0] != transcode.ID {
		t.Fatalf("expiration hooks ran for %v, want the transcode", ended)
	}
}

// The household outcome on the Postgres store: a killed app's quiet paused
// transcode still holds the household's only transcode after its lease
// would have run out, so a login's transcode is refused.
func TestHeldTranscodeKeepsItsHouseholdSlot(t *testing.T) {
	_, pool := householdPoolTestStore(t)
	h := newTestHousehold(t, pool, 1)
	h.setPool(3, 1)
	cleanHouseholdReservations(t, pool, h)
	mainNode, loginNode := householdTestNode(pool), householdTestNode(pool)
	held, err := mainNode.StartSession(h.main, "main-profile", 1, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	quietPausedSession(mainNode, held.ID)
	// The lease is nearly over, with no client traffic to renew it.
	if _, err := pool.Exec(context.Background(), `UPDATE playback_capacity_reservations SET lease_until = now() + interval '1 second' WHERE session_id = $1`, held.ID); err != nil {
		t.Fatal(err)
	}
	mainNode.mu.Lock()
	mainNode.sessions[held.ID].reservationLeaseUntil = time.Now().Add(time.Second)
	mainNode.mu.Unlock()

	mainNode.CleanStale()
	var remaining float64
	if err := pool.QueryRow(context.Background(), `SELECT EXTRACT(EPOCH FROM lease_until - now())::float8 FROM playback_capacity_reservations WHERE session_id = $1`, held.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 30 {
		t.Fatalf("the held transcode's lease has %.1fs left after cleanup, want it renewed", remaining)
	}
	if _, err := loginNode.StartSession(h.logins[0], "login-profile", 1, PlayTranscode, false); !errors.Is(err, ErrHouseholdTranscodesExceeded) {
		t.Fatalf("a login's transcode beside a held one in a 1-transcode household = %v, want the household's transcode limit", err)
	}
}
