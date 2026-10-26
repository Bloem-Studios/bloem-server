package main

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/worker"
)

type lifecycleWorkerStub struct {
	started chan struct{}
	stopped chan struct{}
}

func (w *lifecycleWorkerStub) Run(ctx context.Context) {
	close(w.started)
	<-ctx.Done()
	close(w.stopped)
}

func TestStartAdminPeopleBackgroundWorkerOwnsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &lifecycleWorkerStub{started: make(chan struct{}), stopped: make(chan struct{})}

	startAdminPeopleBackgroundWorker(ctx, worker)
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("admin people worker did not start")
	}

	cancel()
	select {
	case <-worker.stopped:
	case <-time.After(time.Second):
		t.Fatal("admin people worker did not stop with application context")
	}
}

type workerShutdownRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *workerShutdownRecorder) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *workerShutdownRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type reconcilerShutdownStub struct {
	recorder *workerShutdownRecorder
	waitErr  error
}

func (s *reconcilerShutdownStub) Stop() {
	s.recorder.record("reconciler-stop")
}

func (s *reconcilerShutdownStub) StopAndWait(context.Context) error {
	s.recorder.record("reconciler-wait")
	return s.waitErr
}

type heartbeatShutdownStub struct {
	recorder   *workerShutdownRecorder
	waitErr    error
	cleanupErr error
}

func (s *heartbeatShutdownStub) Stop() {
	s.recorder.record("heartbeat-stop")
}

func (s *heartbeatShutdownStub) StopAndWait(context.Context) error {
	s.recorder.record("heartbeat-wait")
	return s.waitErr
}

func (s *heartbeatShutdownStub) CleanupSelf(context.Context) error {
	s.recorder.record("cleanup")
	return s.cleanupErr
}

func TestShutdownSharedWorkerOwnershipSignalsAndJoinsBeforeCleanup(t *testing.T) {
	recorder := &workerShutdownRecorder{}
	reconciler := &reconcilerShutdownStub{recorder: recorder}
	heartbeat := &heartbeatShutdownStub{recorder: recorder}

	result := shutdownSharedWorkerOwnership(context.Background(), reconciler, heartbeat)
	if result.reconcilerJoinErr != nil || result.heartbeatJoinErr != nil || result.cleanupErr != nil {
		t.Fatalf("shutdown result = %+v, want no errors", result)
	}
	want := []string{"reconciler-stop", "heartbeat-stop", "reconciler-wait", "heartbeat-wait", "cleanup"}
	if got := recorder.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("shutdown events = %v, want %v", got, want)
	}
}

func TestShutdownSharedWorkerOwnershipSkipsCleanupAfterEitherJoinError(t *testing.T) {
	reconcilerErr := errors.New("reconciler blocked")
	heartbeatErr := errors.New("heartbeat blocked")
	tests := []struct {
		name              string
		reconcilerWaitErr error
		heartbeatWaitErr  error
	}{
		{name: "reconciler join", reconcilerWaitErr: reconcilerErr},
		{name: "heartbeat join", heartbeatWaitErr: heartbeatErr},
		{name: "both joins", reconcilerWaitErr: reconcilerErr, heartbeatWaitErr: heartbeatErr},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &workerShutdownRecorder{}
			reconciler := &reconcilerShutdownStub{recorder: recorder, waitErr: tc.reconcilerWaitErr}
			heartbeat := &heartbeatShutdownStub{recorder: recorder, waitErr: tc.heartbeatWaitErr}

			result := shutdownSharedWorkerOwnership(context.Background(), reconciler, heartbeat)
			if !errors.Is(result.reconcilerJoinErr, tc.reconcilerWaitErr) {
				t.Fatalf("reconciler join error = %v, want %v", result.reconcilerJoinErr, tc.reconcilerWaitErr)
			}
			if !errors.Is(result.heartbeatJoinErr, tc.heartbeatWaitErr) {
				t.Fatalf("heartbeat join error = %v, want %v", result.heartbeatJoinErr, tc.heartbeatWaitErr)
			}
			if result.cleanupErr != nil {
				t.Fatalf("cleanup error = %v, want nil when cleanup is skipped", result.cleanupErr)
			}
			want := []string{"reconciler-stop", "heartbeat-stop", "reconciler-wait", "heartbeat-wait"}
			if got := recorder.snapshot(); !slices.Equal(got, want) {
				t.Fatalf("shutdown events = %v, want %v", got, want)
			}
		})
	}
}

func TestBloemShutdownSharedWorkerOwnershipAcceptsNilConcreteWriters(t *testing.T) {
	var reconciler *worker.Reconciler
	var heartbeat *worker.HeartbeatWriter
	bloemShutdownSharedWorkerOwnership(t.Context(), reconciler, heartbeat)
}
