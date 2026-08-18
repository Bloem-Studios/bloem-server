package worker

import (
	"context"
	"testing"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// CleanStale must not touch the database (its pool is left nil so any query
// would panic) when another replica already holds the advisory lock for
// this tick.
func TestSessionCleaner_CleanStale_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var lockCalls int
	c := &SessionCleaner{
		// pool intentionally nil: reaching any DELETE/SELECT without the
		// lock check short-circuiting first would panic on a nil pool,
		// turning a logic bug into a hard test failure.
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			lockCalls++
			if key != sessionCleanupLockKey {
				t.Fatalf("unexpected lock key %d, want %d", key, sessionCleanupLockKey)
			}
			return nil, false, nil
		},
	}

	deleted, err := c.CleanStale(context.Background())
	if err != nil {
		t.Fatalf("CleanStale returned error on lock-denied skip: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted rows when the run is skipped, got %d", deleted)
	}
	if lockCalls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", lockCalls)
	}
}

// A lock-acquisition error must also short-circuit before touching the pool.
func TestSessionCleaner_CleanStale_ErrorsOnAdvisoryLockFailure(t *testing.T) {
	c := &SessionCleaner{
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			return nil, false, context.DeadlineExceeded
		},
	}

	if _, err := c.CleanStale(context.Background()); err == nil {
		t.Fatal("expected CleanStale to surface the advisory lock error")
	}
}
