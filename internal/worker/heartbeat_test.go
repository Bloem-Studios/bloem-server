package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type heartbeatStoreFunc func(context.Context, string, ...any) (pgconn.CommandTag, error)

func (f heartbeatStoreFunc) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return f(ctx, sql, args...)
}

func TestHeartbeatWriterBeatUsesNarrowStore(t *testing.T) {
	storeErr := errors.New("store unavailable")
	store := heartbeatStoreFunc(func(context.Context, string, ...any) (pgconn.CommandTag, error) {
		return pgconn.CommandTag{}, storeErr
	})
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")

	err := writer.Beat(context.Background())
	if !errors.Is(err, storeErr) {
		t.Fatalf("Beat error = %v, want wrapped store error", err)
	}
}

type controlledHeartbeatStore struct {
	firstBeatStarted  chan struct{}
	firstBeatCanceled chan struct{}
	firstBeatDone     chan struct{}
	releaseFirstBeat  chan struct{}

	firstBeatOnce sync.Once
	canceledOnce  sync.Once
	releaseOnce   sync.Once
	mu            sync.Mutex
	events        []string
}

func newControlledHeartbeatStore(blockFirstBeat bool) *controlledHeartbeatStore {
	store := &controlledHeartbeatStore{
		firstBeatStarted:  make(chan struct{}),
		firstBeatCanceled: make(chan struct{}),
		firstBeatDone:     make(chan struct{}),
		releaseFirstBeat:  make(chan struct{}),
	}
	if !blockFirstBeat {
		store.releaseFirst()
	}
	return store
}

func (s *controlledHeartbeatStore) Exec(ctx context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO node_heartbeats"):
		first := false
		s.firstBeatOnce.Do(func() {
			first = true
			close(s.firstBeatStarted)
		})
		if first {
			stopWatching := context.AfterFunc(ctx, func() {
				s.canceledOnce.Do(func() { close(s.firstBeatCanceled) })
			})
			<-s.releaseFirstBeat
			_ = stopWatching()
		}
		s.record("heartbeat")
		if first {
			close(s.firstBeatDone)
		}
	case strings.Contains(sql, "DELETE FROM playback_sessions_sync"):
		s.record("sessions-cleanup")
	case strings.Contains(sql, "DELETE FROM node_heartbeats"):
		s.record("heartbeat-cleanup")
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected heartbeat store query: %s", sql)
	}
	return pgconn.CommandTag{}, nil
}

func (s *controlledHeartbeatStore) releaseFirst() {
	s.releaseOnce.Do(func() { close(s.releaseFirstBeat) })
}

func (s *controlledHeartbeatStore) record(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *controlledHeartbeatStore) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func heartbeatTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitForHeartbeatSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", name, ctx.Err())
	}
}

func waitForHeartbeatResult(t *testing.T, ctx context.Context, result <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", name, ctx.Err())
		return nil
	}
}

func TestHeartbeatWriterStartIsIdempotent(t *testing.T) {
	store := newControlledHeartbeatStore(false)
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")
	writer.interval = time.Hour

	const callers = 32
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			writer.Start()
		}()
	}
	close(start)
	workers.Wait()

	ctx := heartbeatTestContext(t)
	waitForHeartbeatSignal(t, ctx, store.firstBeatDone, "initial heartbeat")
	if err := writer.StopAndWait(ctx); err != nil {
		t.Fatalf("StopAndWait: %v", err)
	}
	if got := store.snapshot(); !slices.Equal(got, []string{"heartbeat"}) {
		t.Fatalf("heartbeat events = %v, want one heartbeat", got)
	}
}

func TestHeartbeatWriterStopBeforeStartPreventsLaunch(t *testing.T) {
	store := newControlledHeartbeatStore(false)
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")

	const callers = 32
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			writer.Stop()
		}()
	}
	close(start)
	workers.Wait()

	writer.Start()
	writer.Start()
	ctx := heartbeatTestContext(t)
	if err := writer.StopAndWait(ctx); err != nil {
		t.Fatalf("StopAndWait after stop-before-start: %v", err)
	}
	if got := store.snapshot(); len(got) != 0 {
		t.Fatalf("heartbeat events after stop-before-start = %v, want none", got)
	}
}

func TestHeartbeatWriterConcurrentStopAndWaitJoinsOneLoop(t *testing.T) {
	store := newControlledHeartbeatStore(true)
	t.Cleanup(store.releaseFirst)
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")
	writer.interval = time.Hour
	writer.Start()

	ctx := heartbeatTestContext(t)
	waitForHeartbeatSignal(t, ctx, store.firstBeatStarted, "blocked heartbeat")

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var workers sync.WaitGroup
	workers.Add(callers)
	for range callers {
		go func() {
			defer workers.Done()
			<-start
			results <- writer.StopAndWait(ctx)
		}()
	}
	close(start)
	waitForHeartbeatSignal(t, ctx, store.firstBeatCanceled, "heartbeat cancellation")
	select {
	case err := <-results:
		t.Fatalf("StopAndWait returned before the beat finished: %v", err)
	default:
	}

	store.releaseFirst()
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent StopAndWait: %v", err)
		}
	}
	if err := writer.StopAndWait(ctx); err != nil {
		t.Fatalf("repeated StopAndWait: %v", err)
	}
	if got := store.snapshot(); !slices.Equal(got, []string{"heartbeat"}) {
		t.Fatalf("heartbeat events = %v, want one heartbeat", got)
	}
}

func TestHeartbeatWriterStopAndWaitMakesCleanupFinalWrite(t *testing.T) {
	store := newControlledHeartbeatStore(true)
	t.Cleanup(store.releaseFirst)
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")
	writer.interval = time.Hour
	writer.Start()

	ctx := heartbeatTestContext(t)
	waitForHeartbeatSignal(t, ctx, store.firstBeatStarted, "blocked heartbeat")
	shutdownResult := make(chan error, 1)
	go func() {
		if err := writer.StopAndWait(ctx); err != nil {
			shutdownResult <- err
			return
		}
		shutdownResult <- writer.CleanupSelf(ctx)
	}()
	waitForHeartbeatSignal(t, ctx, store.firstBeatCanceled, "heartbeat cancellation")
	if got := store.snapshot(); len(got) != 0 {
		t.Fatalf("writes before blocked heartbeat finished = %v, want none", got)
	}
	select {
	case err := <-shutdownResult:
		t.Fatalf("shutdown returned before the beat finished: %v", err)
	default:
	}

	store.releaseFirst()
	if err := waitForHeartbeatResult(t, ctx, shutdownResult, "ordered heartbeat shutdown"); err != nil {
		t.Fatalf("ordered heartbeat shutdown: %v", err)
	}
	want := []string{"heartbeat", "sessions-cleanup", "heartbeat-cleanup"}
	if got := store.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("heartbeat shutdown writes = %v, want %v", got, want)
	}
}

func TestHeartbeatWriterStopAndWaitDeadlineAllowsLaterJoin(t *testing.T) {
	store := newControlledHeartbeatStore(true)
	t.Cleanup(store.releaseFirst)
	writer := newHeartbeatWriter(store, "node-a", "integrated", "http://node-a")
	writer.interval = time.Hour
	writer.Start()

	testCtx := heartbeatTestContext(t)
	waitForHeartbeatSignal(t, testCtx, store.firstBeatStarted, "blocked heartbeat")
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := writer.StopAndWait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopAndWait error = %v, want context.DeadlineExceeded", err)
	}
	waitForHeartbeatSignal(t, testCtx, store.firstBeatCanceled, "heartbeat cancellation")

	laterResult := make(chan error, 1)
	go func() { laterResult <- writer.StopAndWait(testCtx) }()
	select {
	case err := <-laterResult:
		t.Fatalf("later StopAndWait returned before the beat finished: %v", err)
	default:
	}

	store.releaseFirst()
	if err := waitForHeartbeatResult(t, testCtx, laterResult, "later heartbeat join"); err != nil {
		t.Fatalf("later StopAndWait: %v", err)
	}
}
