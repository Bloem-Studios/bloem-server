package planstore

import (
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/google/uuid"
)

func TestBloemRemoteStopReleasesCapacityAndReapsProducer(t *testing.T) {
	f := newPlanstoreFixture(t)
	ctx := t.Context()
	plans := NewPostgres(f.pool)
	id := uuid.NewString()
	record := f.attemptRecord(id, uuid.NewString(), "digest")
	if err := plans.SaveAttempt(ctx, record); err != nil {
		t.Fatal(err)
	}
	leases := playback.NewPostgresReservationStore(f.pool)
	request := playback.ReservationRequest{SessionID: id, AccountID: record.UserID, ProfileID: record.ProfileID, LeaseUntil: time.Now().Add(time.Minute), AccountStreams: 1}
	if _, err := leases.Acquire(ctx, request); err != nil {
		t.Fatal(err)
	}
	owner := playback.NewSessionManager(1, 1)
	owner.SetReservationStore(leases, time.Minute)
	owner.RegisterReconstructed(&playback.Session{ID: id, UserID: record.UserID, ProfileID: record.ProfileID, PlayMethod: playback.PlayDirect})
	stop, release := owner.WatchTransportStop(id)
	defer release()
	hooks := 0
	owner.AddRemoteStopHook(func(s *playback.Session) {
		hooks++
		if s.ID != id {
			t.Error("wrong producer stopped")
		}
	})
	// A different replica has only the shared store, not the owner's manager.
	if _, _, err := NewPostgres(f.pool).StopAttempt(ctx, id, uuid.NewString(), nil); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM playback_capacity_reservations WHERE session_id=$1`, id).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("capacity retained: %d %v", remaining, err)
	}
	if _, err := leases.Acquire(ctx, request); !errors.Is(err, playback.ErrAttemptStoppedV3) {
		t.Fatalf("stopped lease resurrected: %v", err)
	}
	if err := owner.ReapStoppedSessions(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stop:
	default:
		t.Fatal("remote stop left transport running")
	}
	if _, err := owner.GetSession(id); !errors.Is(err, playback.ErrSessionNotFound) {
		t.Fatalf("remote producer retained: %v", err)
	}
	if err := owner.ReapStoppedSessions(ctx); err != nil || hooks != 1 {
		t.Fatalf("repeat reap hooks=%d error=%v", hooks, err)
	}
	if _, err := plans.ApplyProgress(ctx, id, playback.ProgressSampleV3{Sequence: 1, Position: 10}); !errors.Is(err, playback.ErrAttemptStoppedV3) {
		t.Fatalf("late progress revived stop: %v", err)
	}
	request.SessionID = uuid.NewString()
	lease, err := leases.Acquire(ctx, request)
	if err != nil {
		t.Fatalf("next viewer cannot claim freed slot: %v", err)
	}
	defer func() {
		if err := leases.Release(ctx, request.SessionID, lease.Generation); err != nil {
			t.Error(err)
		}
	}()
}
