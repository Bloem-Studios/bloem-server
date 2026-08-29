package worker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSyncNowSerializesSnapshotCapture guards the SyncNow ordering contract:
// snapshot capture and reconciliation run under one lock, so a request-path
// sync (playback start/stop) can never interleave with the periodic tick and
// commit an older session snapshot after a newer one.
func TestSyncNowSerializesSnapshotCapture(t *testing.T) {
	var inflight atomic.Int32
	var overlapped atomic.Bool
	provider := func() []SessionSync {
		if inflight.Add(1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(2 * time.Millisecond)
		inflight.Add(-1)
		return nil
	}

	// No pool is needed: an empty snapshot with no node name returns before
	// any database work, keeping the test focused on the locking contract.
	r := NewReconciler(nil, "", provider)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.SyncNow(context.Background()); err != nil {
				t.Errorf("SyncNow: %v", err)
			}
		}()
	}
	wg.Wait()

	if overlapped.Load() {
		t.Fatal("concurrent SyncNow calls captured snapshots concurrently; capture and reconcile must be serialized")
	}
}

// TestSessionSnapshotsEqualDetectsToneMapChanges verifies execution-mode changes trigger reconciliation.
func TestSessionSnapshotsEqualDetectsToneMapChanges(t *testing.T) {
	base := SessionSync{SessionID: "session-1", ToneMapMode: "hardware"}
	if !sessionSnapshotsEqual([]SessionSync{base}, []SessionSync{base}) {
		t.Fatal("identical tone-map facts must compare equal")
	}
	changed := base
	changed.ToneMapMode = "software"
	if sessionSnapshotsEqual([]SessionSync{base}, []SessionSync{changed}) {
		t.Fatal("tone-map mode change must invalidate the session snapshot")
	}
}

// TestSyncNowCoalescesPendingPass guards the follow-up contract: a SyncNow
// call that arrives while a sync is in flight returns immediately, and the
// running owner re-captures a fresh snapshot afterwards — so a stop that lands
// mid-sync is still reflected without waiting for the periodic tick.
func TestSyncNowCoalescesPendingPass(t *testing.T) {
	captures := make(chan struct{}, 16)
	release := make(chan struct{})
	first := true
	provider := func() []SessionSync {
		captures <- struct{}{}
		if first {
			first = false
			<-release // hold the first sync mid-flight
		}
		return nil
	}
	r := NewReconciler(nil, "", provider)

	ownerDone := make(chan error, 1)
	go func() { ownerDone <- r.SyncNow(context.Background()) }()
	<-captures // owner is now blocked inside its snapshot capture

	// A second sync while the first is in flight must not block.
	done := make(chan struct{})
	go func() {
		_ = r.SyncNow(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SyncNow blocked behind an in-flight sync; it must coalesce and return")
	}

	close(release)
	if err := <-ownerDone; err != nil {
		t.Fatalf("owner SyncNow: %v", err)
	}
	select {
	case <-captures: // the owner's follow-up pass with a fresh snapshot
	default:
		t.Fatal("no follow-up snapshot capture ran; the coalesced sync was lost")
	}
}

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
