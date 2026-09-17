package playback

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fleetStoreGate parks one store call until the test releases it, so a test
// can observe what the manager does while a database round trip is in flight.
type fleetStoreGate struct {
	entered chan struct{}
	release chan struct{}
}

func newFleetStoreGate() *fleetStoreGate {
	return &fleetStoreGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

// generationStore models the Postgres store's fencing: one row per session,
// generations from a single sequence, Renew and Release only for the current
// generation.
type generationStore struct {
	mu               sync.Mutex
	next             int64
	rows             map[string]int64
	acquires         []ReservationRequest
	gate             *fleetStoreGate
	staleAcquires    int
	failNextAcquire  error
	missingDeadlines []string
}

func newGenerationStore() *generationStore {
	return &generationStore{rows: map[string]int64{}}
}

func (s *generationStore) noteDeadline(ctx context.Context, op string) {
	if _, ok := ctx.Deadline(); !ok {
		s.missingDeadlines = append(s.missingDeadlines, op)
	}
}

func (s *generationStore) Acquire(ctx context.Context, request ReservationRequest) (Reservation, error) {
	s.mu.Lock()
	s.noteDeadline(ctx, "acquire")
	s.acquires = append(s.acquires, request)
	if err := s.failNextAcquire; err != nil {
		s.failNextAcquire = nil
		s.mu.Unlock()
		return Reservation{}, err
	}
	gate := s.gate
	s.gate = nil
	leaseUntil := request.LeaseUntil
	if s.staleAcquires > 0 {
		s.staleAcquires--
		leaseUntil = time.Now()
	}
	s.mu.Unlock()

	if gate != nil {
		gate.entered <- struct{}{}
		<-gate.release
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.rows[request.SessionID] = s.next
	return Reservation{SessionID: request.SessionID, Generation: s.next, LeaseUntil: leaseUntil}, nil
}

func (s *generationStore) Renew(ctx context.Context, sessionID string, generation int64, leaseUntil time.Time) (Reservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noteDeadline(ctx, "renew")
	if s.rows[sessionID] != generation {
		return Reservation{}, ErrReservationGenerationMismatch
	}
	return Reservation{SessionID: sessionID, Generation: generation, LeaseUntil: leaseUntil}, nil
}

func (s *generationStore) Release(ctx context.Context, sessionID string, generation int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noteDeadline(ctx, "release")
	if s.rows[sessionID] != generation {
		return ErrReservationGenerationMismatch
	}
	delete(s.rows, sessionID)
	return nil
}

func (s *generationStore) armGate() *fleetStoreGate {
	gate := newFleetStoreGate()
	s.mu.Lock()
	s.gate = gate
	s.mu.Unlock()
	return gate
}

// dropRow simulates the lease expiring and another node sweeping the row.
func (s *generationStore) dropRow(sessionID string) {
	s.mu.Lock()
	delete(s.rows, sessionID)
	s.mu.Unlock()
}

func (s *generationStore) row(sessionID string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, ok := s.rows[sessionID]
	return generation, ok
}

func (s *generationStore) acquireCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.acquires)
}

// awaitSignal fails the test instead of hanging when an expected event never
// arrives. The timeout is a failure guard, not a synchronization mechanism.
func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func runAsync(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

func reservationGeneration(t *testing.T, manager *SessionManager, sessionID string) int64 {
	t.Helper()
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	session := manager.sessions[sessionID]
	if session == nil {
		t.Fatalf("session %s not found", sessionID)
	}
	return session.reservationGeneration
}

func startReplacementFixture(t *testing.T) (*SessionManager, *generationStore, *Session, *Session) {
	t.Helper()
	store := newGenerationStore()
	manager := NewSessionManager(0, 0)
	manager.SetReservationStore(store, time.Minute)
	replaced, err := manager.StartSession(7, "profile-1", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	bystander, err := manager.StartSession(8, "profile-2", 43, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CheckReplacementAllowed(context.Background(), replaced.ID, PlayTranscode, false); err != nil {
		t.Fatal(err)
	}
	return manager, store, replaced, bystander
}

// A stalled restore used to run under the manager's global mutex with no
// deadline, freezing progress, activity, and transport for every session.
func TestCancelReplacementRestoreDoesNotBlockOtherSessions(t *testing.T) {
	manager, store, replaced, bystander := startReplacementFixture(t)

	gate := store.armGate()
	canceled := runAsync(func() { manager.CancelReplacementReservation(replaced.ID) })
	awaitSignal(t, gate.entered, "restore to reach the store")

	progressed := runAsync(func() {
		if err := manager.UpdateProgress(bystander.ID, 12, false); err != nil {
			t.Errorf("UpdateProgress: %v", err)
		}
	})
	awaitSignal(t, progressed, "bystander progress while restore is in flight")

	close(gate.release)
	awaitSignal(t, canceled, "cancel to finish")

	generation, ok := store.row(replaced.ID)
	if !ok || generation != reservationGeneration(t, manager, replaced.ID) {
		t.Fatalf("store generation %d (present=%v) does not match the session", generation, ok)
	}
	if last := store.acquires[len(store.acquires)-1]; last.IsTranscode {
		t.Fatalf("cancel restored %#v, want the direct reservation", last)
	}
}

func TestRollbackReplacementRestoreDoesNotBlockOtherSessions(t *testing.T) {
	manager, store, replaced, bystander := startReplacementFixture(t)
	rollback, err := manager.ApplyReplacement(replaced.ID, SessionReplacement{
		EffectiveMediaFileID: 44,
		StreamState: SessionStreamState{
			PlayMethod:        PlayTranscode,
			BasePlayMethod:    PlayTranscode,
			TranscodeRouteSet: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	gate := store.armGate()
	rolledBack := make(chan error, 1)
	go func() { rolledBack <- manager.RollbackReplacement(replaced.ID, rollback) }()
	awaitSignal(t, gate.entered, "rollback restore to reach the store")

	touched := runAsync(func() {
		if err := manager.TouchActivity(bystander.ID); err != nil {
			t.Errorf("TouchActivity: %v", err)
		}
	})
	awaitSignal(t, touched, "bystander activity while rollback restore is in flight")

	close(gate.release)
	select {
	case err := <-rolledBack:
		if err != nil {
			t.Fatalf("RollbackReplacement: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rollback")
	}
	session, err := manager.GetSession(replaced.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.MediaFileID != 42 || session.PlayMethod != PlayDirect {
		t.Fatalf("rollback did not restore stream state: file=%d method=%s", session.MediaFileID, session.PlayMethod)
	}
	generation, ok := store.row(replaced.ID)
	if !ok || generation != reservationGeneration(t, manager, replaced.ID) {
		t.Fatalf("store generation %d (present=%v) does not match the session", generation, ok)
	}
}

// A renewal that reacquired a lost lease while the session stopped used to
// leave the new row behind, holding capacity until the lease expired.
func TestRenewReacquireRacingStopReleasesNewGeneration(t *testing.T) {
	store := newGenerationStore()
	store.staleAcquires = 1
	manager := NewSessionManager(0, 0)
	manager.SetReservationStore(store, time.Minute)
	session, err := manager.StartSession(7, "profile-1", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	store.dropRow(session.ID)

	gate := store.armGate()
	touched := runAsync(func() { _ = manager.TouchActivity(session.ID) })
	awaitSignal(t, gate.entered, "renewal to reacquire the lost lease")

	if err := manager.StopSession(session.ID); err != nil {
		t.Fatal(err)
	}
	close(gate.release)
	awaitSignal(t, touched, "renewal to finish")

	if generation, ok := store.row(session.ID); ok {
		t.Fatalf("stopped session still holds fleet generation %d", generation)
	}
}

// Concurrent requests that all observe a lost lease must reacquire once and
// leave the manager tracking the generation the store kept, so a later stop
// releases it.
func TestConcurrentRenewalsReacquireOnceAndStopReleases(t *testing.T) {
	store := newGenerationStore()
	store.staleAcquires = 1
	manager := NewSessionManager(0, 0)
	manager.SetReservationStore(store, time.Minute)
	session, err := manager.StartSession(7, "profile-1", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	store.dropRow(session.ID)

	gate := store.armGate()
	first := runAsync(func() { _ = manager.TouchActivity(session.ID) })
	awaitSignal(t, gate.entered, "first renewal to reacquire")

	var others sync.WaitGroup
	for range 8 {
		others.Add(1)
		go func() {
			defer others.Done()
			_ = manager.TouchActivity(session.ID)
		}()
	}
	close(gate.release)
	awaitSignal(t, first, "first renewal")
	othersDone := runAsync(others.Wait)
	awaitSignal(t, othersDone, "concurrent renewals")

	if got := store.acquireCount(); got != 2 {
		t.Fatalf("acquires = %d, want start plus exactly one reacquire", got)
	}
	generation, ok := store.row(session.ID)
	if !ok || generation != reservationGeneration(t, manager, session.ID) {
		t.Fatalf("store generation %d (present=%v) does not match the session", generation, ok)
	}
	if err := manager.StopSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if generation, ok := store.row(session.ID); ok {
		t.Fatalf("stop left fleet generation %d behind", generation)
	}
}

func TestFleetReservationStoreCallsAreBounded(t *testing.T) {
	manager, store, replaced, _ := startReplacementFixture(t)
	manager.CancelReplacementReservation(replaced.ID)
	store.dropRow(replaced.ID)
	manager.mu.Lock()
	manager.sessions[replaced.ID].reservationLeaseUntil = time.Now()
	manager.mu.Unlock()
	if err := manager.TouchActivity(replaced.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.StopSession(replaced.ID); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.missingDeadlines) != 0 {
		t.Fatalf("store calls without a deadline: %v", store.missingDeadlines)
	}
}
