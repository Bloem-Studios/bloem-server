package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type recordingReservationStore struct {
	mu         sync.Mutex
	acquires   []ReservationRequest
	renews     []Reservation
	releases   []Reservation
	acquireErr error
}

type leaseLossReservationStore struct {
	acquireCalls int
}

func (store *leaseLossReservationStore) Acquire(_ context.Context, request ReservationRequest) (Reservation, error) {
	store.acquireCalls++
	if store.acquireCalls > 1 {
		return Reservation{}, ErrTooManyStreams
	}
	return Reservation{SessionID: request.SessionID, Generation: 1, LeaseUntil: time.Now()}, nil
}

func (*leaseLossReservationStore) Renew(context.Context, string, int64, time.Time) (Reservation, error) {
	return Reservation{}, ErrReservationGenerationMismatch
}

func (*leaseLossReservationStore) Release(context.Context, string, int64) error {
	return ErrReservationGenerationMismatch
}

func (store *recordingReservationStore) Acquire(_ context.Context, request ReservationRequest) (Reservation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.acquires = append(store.acquires, request)
	if store.acquireErr != nil {
		return Reservation{}, store.acquireErr
	}
	return Reservation{SessionID: request.SessionID, Generation: int64(len(store.acquires)), LeaseUntil: time.Now()}, nil
}

func (store *recordingReservationStore) Renew(_ context.Context, sessionID string, generation int64, leaseUntil time.Time) (Reservation, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	reservation := Reservation{SessionID: sessionID, Generation: generation, LeaseUntil: leaseUntil}
	store.renews = append(store.renews, reservation)
	return reservation, nil
}

func (store *recordingReservationStore) Release(_ context.Context, sessionID string, generation int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.releases = append(store.releases, Reservation{SessionID: sessionID, Generation: generation})
	return nil
}

func TestSessionManagerBindsStartHeartbeatAndStopToFleetReservation(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 1, TenantID: "tenant-1", TenantMaxTranscodes: 2}, nil
	})

	session, err := manager.StartSession(7, "profile-1", 42, PlayTranscode, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.acquires) != 1 {
		t.Fatalf("acquires = %d", len(store.acquires))
	}
	request := store.acquires[0]
	if request.SessionID != session.ID || request.AccountID != 7 || request.ProfileID != "profile-1" || request.TenantID != "tenant-1" || !request.IsTranscode || request.AccountStreams != 3 || request.AccountTranscodes != 1 || request.TenantTranscodes != 2 {
		t.Fatalf("request = %#v", request)
	}
	if err := manager.UpdateProgress(session.ID, 5, false); err != nil {
		t.Fatal(err)
	}
	if len(store.renews) != 1 || store.renews[0].SessionID != session.ID {
		t.Fatalf("renews = %#v", store.renews)
	}
	if err := manager.StopSession(session.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.releases) != 1 || store.releases[0].SessionID != session.ID || store.releases[0].Generation <= 0 {
		t.Fatalf("releases = %#v", store.releases)
	}
}

func TestSessionManagerDoesNotPublishSessionWhenFleetReservationFails(t *testing.T) {
	store := &recordingReservationStore{acquireErr: ErrTenantTranscodesExceeded}
	manager := NewSessionManager(0, 0)
	manager.SetReservationStore(store, time.Minute)

	if _, err := manager.StartSession(7, "profile-1", 42, PlayTranscode, false); !errors.Is(err, ErrTenantTranscodesExceeded) {
		t.Fatalf("StartSession = %v", err)
	}
	manager.mu.RLock()
	published := len(manager.sessions)
	manager.mu.RUnlock()
	if published != 0 {
		t.Fatalf("published sessions = %d", published)
	}
}

func TestSessionManagerReconstructionReacquiresFleetReservation(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(2, 1)
	manager.SetReservationStore(store, time.Minute)

	session, err := manager.RegisterReconstructedWithLimits(context.Background(), &Session{
		ID: "reconstructed-session", UserID: 9, ProfileID: "profile-9", PlayMethod: PlayDirect,
	})
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || len(store.acquires) != 1 || store.acquires[0].SessionID != "reconstructed-session" {
		t.Fatalf("session=%#v acquires=%#v", session, store.acquires)
	}
}

func TestSessionManagerFailsClosedWhenExpiredLeaseCannotBeReacquired(t *testing.T) {
	store := &leaseLossReservationStore{}
	manager := NewSessionManager(1, 0)
	manager.SetReservationStore(store, time.Minute)
	session, err := manager.StartSession(7, "profile-1", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateProgress(session.ID, 5, false); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("UpdateProgress = %v", err)
	}
	if _, err := manager.GetSession(session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session survived lost fleet lease: %v", err)
	}
}

func TestSessionManagerReplacementReservationCommitsCancelsAndRollsBack(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(2, 1)
	manager.SetReservationStore(store, time.Minute)
	session, err := manager.StartSession(7, "profile-1", 42, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.CheckReplacementAllowed(context.Background(), session.ID, PlayTranscode, false); err != nil {
		t.Fatal(err)
	}
	if got := store.acquires[len(store.acquires)-1]; !got.IsTranscode {
		t.Fatalf("replacement reservation = %#v", got)
	}
	manager.CancelReplacementReservation(session.ID)
	if got := store.acquires[len(store.acquires)-1]; got.IsTranscode {
		t.Fatalf("cancel did not restore direct reservation: %#v", got)
	}

	if err := manager.CheckReplacementAllowed(context.Background(), session.ID, PlayTranscode, false); err != nil {
		t.Fatal(err)
	}
	rollback, err := manager.ApplyReplacement(session.ID, SessionReplacement{
		EffectiveMediaFileID: 43,
		StreamState: SessionStreamState{
			PlayMethod:        PlayTranscode,
			BasePlayMethod:    PlayTranscode,
			TranscodeRouteSet: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RollbackReplacement(session.ID, rollback); err != nil {
		t.Fatal(err)
	}
	if got := store.acquires[len(store.acquires)-1]; got.IsTranscode {
		t.Fatalf("rollback did not restore direct reservation: %#v", got)
	}
}

// A client that authenticates without an active profile cannot be expressed as
// a ReservationRequest (valid() requires a profile), so gating its start on the
// fleet reservation turned "no profile" into ErrReservationInvalid and refused
// playback that upstream Silo serves. With nothing to meter -- no profile and
// no tenant -- the start must fall back to Silo's in-memory admission instead.
func TestProfilelessNonTenantStartFallsBackToSiloAdmission(t *testing.T) {
	store := &recordingReservationStore{acquireErr: ErrReservationInvalid}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 1}, nil
	})

	session, err := manager.StartSession(7, "", 42, PlayDirect, false)
	if err != nil {
		t.Fatalf("profileless start must succeed like Silo, got %v", err)
	}
	if session == nil || session.ID == "" {
		t.Fatal("expected a session")
	}
	if len(store.acquires) != 0 {
		t.Fatalf("no reservation should be attempted, got %d", len(store.acquires))
	}
	// Teardown must tolerate a session that never reserved.
	if err := manager.StopSession(session.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(store.releases) != 0 {
		t.Fatalf("no release should be attempted, got %d", len(store.releases))
	}
}

// The tenant pool is the thing the reservation defends, so a tenant session
// still reserves even when the caller carries no profile: letting it through
// unmetered would let a paid shared pool be overrun.
func TestProfilelessTenantStartStillReserves(t *testing.T) {
	store := &recordingReservationStore{}
	manager := NewSessionManager(6, 2)
	manager.SetReservationStore(store, time.Minute)
	manager.SetLimitProvider(func(context.Context, int, string) (SessionLimits, error) {
		return SessionLimits{MaxStreams: 3, MaxTranscodes: 1, TenantID: "tenant-1", TenantMaxTranscodes: 2}, nil
	})

	if _, err := manager.StartSession(7, "", 42, PlayTranscode, false); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(store.acquires) != 1 {
		t.Fatalf("tenant session must still reserve, acquires = %d", len(store.acquires))
	}
	if store.acquires[0].TenantID != "tenant-1" {
		t.Fatalf("request = %#v", store.acquires[0])
	}
}
