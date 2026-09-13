package worker

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReconcilerStartIsIdempotentAndStopAndWaitJoinsTickerLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	preSyncEntered := make(chan struct{})
	releasePreSync := make(chan struct{})
	var preSyncOnce sync.Once
	var preSyncCalls atomic.Int32
	reconciler := NewReconciler(nil, "", nil)
	reconciler.interval = time.Nanosecond
	reconciler.PreSync = func() {
		preSyncCalls.Add(1)
		preSyncOnce.Do(func() { close(preSyncEntered) })
		<-releasePreSync
	}

	var starters sync.WaitGroup
	for range 32 {
		starters.Add(1)
		go func() {
			defer starters.Done()
			reconciler.Start()
		}()
	}
	starters.Wait()
	waitForReconcilerSignal(t, ctx, preSyncEntered, "ticker loop to enter PreSync")

	// Install the stop fence synchronously before the blocked hook can resume.
	// A goroutine-start signal alone would not prove StopAndWait had called Stop.
	reconciler.Stop()
	joinStarted := make(chan struct{})
	joinResult := make(chan error, 1)
	go func() {
		close(joinStarted)
		joinResult <- reconciler.StopAndWait(ctx)
	}()
	waitForReconcilerSignal(t, ctx, joinStarted, "ticker join to start")
	select {
	case err := <-joinResult:
		t.Fatalf("StopAndWait returned before the ticker loop finished: %v", err)
	default:
	}

	close(releasePreSync)
	if err := waitForReconcilerResult(t, ctx, joinResult, "ticker loop join"); err != nil {
		t.Fatalf("StopAndWait: %v", err)
	}
	if calls := preSyncCalls.Load(); calls != 1 {
		t.Fatalf("PreSync calls = %d, want exactly one ticker loop", calls)
	}

	// The completed lifecycle remains observable and Start cannot relaunch it.
	reconciler.Start()
	if err := reconciler.StopAndWait(ctx); err != nil {
		t.Fatalf("repeated StopAndWait: %v", err)
	}
	if calls := preSyncCalls.Load(); calls != 1 {
		t.Fatalf("PreSync calls after repeated Start = %d, want 1", calls)
	}
}

func TestReconcilerConcurrentStopBeforeStartPreventsFutureSyncOwnership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var captures atomic.Int32
	reconciler := NewReconciler(nil, "", func() []SessionSync {
		captures.Add(1)
		return nil
	})

	results := make(chan error, 32)
	var stoppers sync.WaitGroup
	for range 32 {
		stoppers.Add(1)
		go func() {
			defer stoppers.Done()
			results <- reconciler.StopAndWait(ctx)
		}()
	}
	stoppers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent StopAndWait: %v", err)
		}
	}

	reconciler.Start()
	if err := reconciler.SyncNow(ctx); err != nil {
		t.Fatalf("SyncNow after stop: %v", err)
	}
	if err := reconciler.StopAndWait(ctx); err != nil {
		t.Fatalf("join after stop-before-start: %v", err)
	}
	if calls := captures.Load(); calls != 0 {
		t.Fatalf("snapshot captures after stop-before-start = %d, want 0", calls)
	}
}

func TestReconcilerStopAndWaitJoinsOwnerAndSuppressesQueuedFollowUp(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstCapture := make(chan struct{})
	releaseFirstCapture := make(chan struct{})
	var captureOnce sync.Once
	var captures atomic.Int32
	reconciler := NewReconciler(nil, "", func() []SessionSync {
		captures.Add(1)
		captureOnce.Do(func() {
			close(firstCapture)
			<-releaseFirstCapture
		})
		return nil
	})

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- reconciler.SyncNow(context.Background()) }()
	waitForReconcilerSignal(t, ctx, firstCapture, "first snapshot capture")

	queuedResult := make(chan error, 1)
	go func() { queuedResult <- reconciler.SyncNow(context.Background()) }()
	if err := waitForReconcilerResult(t, ctx, queuedResult, "queued SyncNow"); err != nil {
		t.Fatalf("queued SyncNow: %v", err)
	}

	// Clear the queued follow-up synchronously before the current owner can
	// resume. The asynchronous wait below still proves that owner is joined.
	reconciler.Stop()
	joinStarted := make(chan struct{})
	joinResult := make(chan error, 1)
	go func() {
		close(joinStarted)
		joinResult <- reconciler.StopAndWait(ctx)
	}()
	waitForReconcilerSignal(t, ctx, joinStarted, "owner join to start")
	select {
	case err := <-joinResult:
		t.Fatalf("StopAndWait returned before the current owner finished: %v", err)
	default:
	}

	close(releaseFirstCapture)
	if err := waitForReconcilerResult(t, ctx, ownerResult, "current SyncNow owner"); err != nil {
		t.Fatalf("current SyncNow owner: %v", err)
	}
	if err := waitForReconcilerResult(t, ctx, joinResult, "current owner join"); err != nil {
		t.Fatalf("StopAndWait: %v", err)
	}
	if calls := captures.Load(); calls != 1 {
		t.Fatalf("snapshot captures after stop = %d, want queued follow-up suppressed", calls)
	}
}

func TestReconcilerStopAndWaitDeadlineAllowsLaterOwnerJoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ownerEntered := make(chan struct{})
	releaseOwner := make(chan struct{})
	var ownerOnce sync.Once
	var captures atomic.Int32
	reconciler := NewReconciler(nil, "", func() []SessionSync {
		captures.Add(1)
		ownerOnce.Do(func() {
			close(ownerEntered)
			<-releaseOwner
		})
		return nil
	})

	ownerResult := make(chan error, 1)
	go func() { ownerResult <- reconciler.SyncNow(context.Background()) }()
	waitForReconcilerSignal(t, ctx, ownerEntered, "external SyncNow owner")

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	if err := reconciler.StopAndWait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopAndWait error = %v, want context.DeadlineExceeded", err)
	}

	laterResult := make(chan error, 1)
	go func() { laterResult <- reconciler.StopAndWait(ctx) }()
	select {
	case err := <-laterResult:
		t.Fatalf("later StopAndWait returned before the external owner finished: %v", err)
	default:
	}

	close(releaseOwner)
	if err := waitForReconcilerResult(t, ctx, ownerResult, "external owner completion"); err != nil {
		t.Fatalf("external SyncNow owner: %v", err)
	}
	if err := waitForReconcilerResult(t, ctx, laterResult, "later owner join"); err != nil {
		t.Fatalf("later StopAndWait: %v", err)
	}

	if err := reconciler.SyncNow(ctx); err != nil {
		t.Fatalf("SyncNow after stop: %v", err)
	}
	if calls := captures.Load(); calls != 1 {
		t.Fatalf("snapshot captures after stop = %d, want 1", calls)
	}
}

func waitForReconcilerSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
	}
}

func waitForReconcilerResult(t *testing.T, ctx context.Context, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
		return ctx.Err()
	}
}
