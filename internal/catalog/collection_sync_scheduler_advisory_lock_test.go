package catalog

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// RunOnce must not touch the repository (list due collections, sync, etc.)
// when another replica already holds the advisory lock for this tick — it
// should log and return an empty, error-free result instead.
func TestCollectionSyncScheduler_RunOnce_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var lockCalls int
	s := &CollectionSyncScheduler{
		// repo intentionally left nil: if RunOnce reached repo.ListDueForSync
		// without acquiring the lock, this test would panic on a nil pointer
		// dereference instead of merely failing an assertion.
		logger: slog.Default(),
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			lockCalls++
			if key != collectionSyncSchedulerLockKey {
				t.Fatalf("unexpected lock key %d, want %d", key, collectionSyncSchedulerLockKey)
			}
			return nil, false, nil
		},
	}

	result, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce returned error on lock-denied skip: %v", err)
	}
	if lockCalls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", lockCalls)
	}

	var decoded CollectionSyncResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if decoded != (CollectionSyncResult{}) {
		t.Fatalf("expected an empty result when the run is skipped, got %+v", decoded)
	}
}

// A lock-acquisition error must also short-circuit before touching the repo.
func TestCollectionSyncScheduler_RunOnce_ErrorsOnAdvisoryLockFailure(t *testing.T) {
	s := &CollectionSyncScheduler{
		logger: slog.Default(),
		tryLockFunc: func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
			return nil, false, context.DeadlineExceeded
		},
	}

	if _, err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("expected RunOnce to surface the advisory lock error")
	}
}
