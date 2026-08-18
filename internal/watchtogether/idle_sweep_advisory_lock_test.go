package watchtogether

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// countingIdleListRepo wraps stubRepo and records how many times
// ListIdleRoomIDs is invoked, so tests can assert the database sweep never
// runs when the advisory lock is denied.
type countingIdleListRepo struct {
	stubRepo
	idleListCalls int
}

func (r *countingIdleListRepo) ListIdleRoomIDs(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	r.idleListCalls++
	return r.stubRepo.ListIdleRoomIDs(ctx, cutoff, limit)
}

// sweepIdleRooms must not query the database for idle rooms when another
// replica already holds the advisory lock for this sweep.
func TestSweepIdleRooms_SkipsDatabaseSweepWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	repo := &countingIdleListRepo{}
	svc := &Service{
		repo:  repo,
		now:   func() time.Time { return time.Now().UTC() },
		rooms: map[string]*liveRoom{},
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			if key != idleRoomSweepLockKey {
				t.Fatalf("unexpected lock key %d, want idleRoomSweepLockKey %d", key, idleRoomSweepLockKey)
			}
			return nil, false, nil
		},
	}

	svc.sweepIdleRooms()

	if repo.idleListCalls != 0 {
		t.Fatalf("expected ListIdleRoomIDs to be skipped, got %d calls", repo.idleListCalls)
	}
}

// When the lock is acquired, the sweep proceeds and queries the database as
// before.
func TestSweepIdleRooms_RunsDatabaseSweepWhenAdvisoryLockAcquired(t *testing.T) {
	repo := &countingIdleListRepo{}
	svc := &Service{
		repo:  repo,
		now:   func() time.Time { return time.Now().UTC() },
		rooms: map[string]*liveRoom{},
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			return &dblock.Lock{}, true, nil
		},
	}

	svc.sweepIdleRooms()

	if repo.idleListCalls != 1 {
		t.Fatalf("expected ListIdleRoomIDs to run exactly once, got %d calls", repo.idleListCalls)
	}
}
