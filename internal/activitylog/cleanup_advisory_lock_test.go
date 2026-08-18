package activitylog

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// panicOnUseStore fails the test immediately if CleanupOnce reaches
// retention-days lookup (i.e. the database) without first being turned away
// by the advisory lock.
type panicOnUseStore struct{ t *testing.T }

func (s panicOnUseStore) Get(ctx context.Context, key string) (string, error) {
	s.t.Fatalf("SettingsStore.Get(%q) called despite the advisory lock being held elsewhere", key)
	return "", nil
}
func (s panicOnUseStore) Set(ctx context.Context, key, value string) error {
	s.t.Fatalf("SettingsStore.Set(%q) called despite the advisory lock being held elsewhere", key)
	return nil
}

func TestCleanupOnce_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var lockCalls int
	tryLockFunc = func(ctx context.Context, pool *pgxpool.Pool, key int64) (*dblock.Lock, bool, error) {
		lockCalls++
		if key != cleanupLockKey {
			t.Fatalf("unexpected lock key %d, want %d", key, cleanupLockKey)
		}
		return nil, false, nil
	}
	t.Cleanup(func() { tryLockFunc = nil })

	deleted := CleanupOnce(context.Background(), nil, panicOnUseStore{t: t}, nil)

	if deleted != 0 {
		t.Fatalf("expected 0 deleted rows when the run is skipped, got %d", deleted)
	}
	if lockCalls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", lockCalls)
	}
}

// A lock-acquisition error must also short-circuit before touching the store.
func TestCleanupOnce_SkipsOnAdvisoryLockError(t *testing.T) {
	tryLockFunc = func(ctx context.Context, pool *pgxpool.Pool, key int64) (*dblock.Lock, bool, error) {
		return nil, false, context.DeadlineExceeded
	}
	t.Cleanup(func() { tryLockFunc = nil })

	deleted := CleanupOnce(context.Background(), nil, panicOnUseStore{t: t}, nil)

	if deleted != 0 {
		t.Fatalf("expected 0 deleted rows on lock error, got %d", deleted)
	}
}
