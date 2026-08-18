package recommendations

import (
	"context"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// newLockTestWorker builds a Worker with no real engine/pool, suitable only
// for exercising the advisory-lock gating around each cron job entry point.
// tryLockFunc stands in for dblock.TryLock so these tests never touch a
// database: they assert that runX() consults the lock and short-circuits
// (never reaching engine-dependent work) exactly when the lock is denied,
// and proceeds when it is granted.
func newLockTestWorker(tryLock func(ctx context.Context, key int64) (*dblock.Lock, bool, error)) *Worker {
	return &Worker{
		running:     make(map[JobName]bool),
		tryLockFunc: tryLock,
	}
}

func TestRunEmbeddings_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var calls int
	w := newLockTestWorker(func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
		calls++
		if key != embeddingsLockKey {
			t.Fatalf("unexpected lock key %d, want embeddingsLockKey %d", key, embeddingsLockKey)
		}
		return nil, false, nil // another replica holds it
	})

	w.runEmbeddings()

	if calls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", calls)
	}
	if w.IsRunning(JobEmbeddings) {
		t.Fatal("job must not be left marked running after a skipped (lock-denied) run")
	}
}

func TestRunTasteProfiles_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var calls int
	w := newLockTestWorker(func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
		calls++
		if key != tasteProfilesLockKey {
			t.Fatalf("unexpected lock key %d, want tasteProfilesLockKey %d", key, tasteProfilesLockKey)
		}
		return nil, false, nil
	})

	w.runTasteProfiles()

	if calls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", calls)
	}
	if w.IsRunning(JobTasteProfiles) {
		t.Fatal("job must not be left marked running after a skipped (lock-denied) run")
	}
}

func TestRunCowatch_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var calls int
	w := newLockTestWorker(func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
		calls++
		if key != cowatchLockKey {
			t.Fatalf("unexpected lock key %d, want cowatchLockKey %d", key, cowatchLockKey)
		}
		return nil, false, nil
	})

	w.runCowatch()

	if calls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", calls)
	}
	if w.IsRunning(JobCowatch) {
		t.Fatal("job must not be left marked running after a skipped (lock-denied) run")
	}
}

func TestRunRecommendations_SkipsWhenAdvisoryLockHeldElsewhere(t *testing.T) {
	var calls int
	w := newLockTestWorker(func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
		calls++
		if key != recommendationsLockKey {
			t.Fatalf("unexpected lock key %d, want recommendationsLockKey %d", key, recommendationsLockKey)
		}
		return nil, false, nil
	})

	w.runRecommendations()

	if calls != 1 {
		t.Fatalf("expected exactly one lock attempt, got %d", calls)
	}
	if w.IsRunning(JobRecommendations) {
		t.Fatal("job must not be left marked running after a skipped (lock-denied) run")
	}
}

// A lock acquisition error (not "held elsewhere", but an actual DB/connection
// error) must also skip the run rather than crash on a nil engine.
func TestRunEmbeddings_SkipsOnAdvisoryLockError(t *testing.T) {
	w := newLockTestWorker(func(ctx context.Context, key int64) (*dblock.Lock, bool, error) {
		return nil, false, context.DeadlineExceeded
	})

	w.runEmbeddings() // must not panic despite w.engine being nil

	if w.IsRunning(JobEmbeddings) {
		t.Fatal("job must not be left marked running after a lock error")
	}
}

// Distinct lock keys are used per job, so unrelated jobs never contend with
// each other over the same advisory lock.
func TestJobLockKeys_AreAllDistinct(t *testing.T) {
	keys := []int64{embeddingsLockKey, tasteProfilesLockKey, cowatchLockKey, recommendationsLockKey}
	seen := make(map[int64]struct{}, len(keys))
	for _, k := range keys {
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate advisory lock key %d among recommendation job keys", k)
		}
		seen[k] = struct{}{}
	}
}

// releaseJobLock must tolerate a nil *dblock.Lock (the shape TryLock returns
// when the lock was not acquired) without panicking — several call sites
// defer it unconditionally.
func TestReleaseJobLock_NilLockIsSafe(t *testing.T) {
	w := newLockTestWorker(nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.releaseJobLock(nil)
	}()
	wg.Wait()
}
