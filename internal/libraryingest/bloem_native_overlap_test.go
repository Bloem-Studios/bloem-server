package libraryingest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/scanner"
)

// Gate only the provider boundary. Executor admission, waiting, cancellation
// and progress reporting remain real.
type gatedNativeIngestFixture struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (*gatedNativeIngestFixture) HasNativeBinding(_ context.Context, id int) (bool, error) {
	return id == 42, nil
}

func (f *gatedNativeIngestFixture) IngestNativeFolder(ctx context.Context, folder *models.MediaFolder) (*Result, error) {
	if folder.ID != 42 || folder.Type != "ebook" || !folder.Enabled || len(folder.Paths) != 0 {
		return nil, errors.New("unexpected native fixture folder")
	}
	if f.calls.Add(1) == 1 {
		close(f.started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.release:
		}
	}
	return &Result{ScanResult: &scanner.ScanResult{New: 1}}, nil
}

func startNativeOverlapIngest(ctx context.Context, executor *Executor) (<-chan ingestOutcome, <-chan string) {
	messages := make(chan string, 64)
	ctx = WithProgressReporter(ctx, func(update ProgressUpdate) {
		select {
		case messages <- update.Message:
		default:
		}
	})
	done := make(chan ingestOutcome, 1)
	go func() {
		result, err := executor.IngestFolder(ctx, &models.MediaFolder{ID: 42})
		done <- ingestOutcome{result: result, err: err}
	}()
	return done, messages
}

func waitNativeOverlapStarted(t *testing.T, fixture *gatedNativeIngestFixture) {
	t.Helper()
	select {
	case <-fixture.started:
	case <-time.After(overlapTestTimeout):
		t.Fatal("native provider did not start")
	}
}

// A second native full scan must run after the first finishes. Returning a
// successful skipped result here would lose the second scan's observation.
func TestNativeStorageOverlapWaitsThenScans(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	executor, _ := newNativeDispatchExecutor()
	fixture := &gatedNativeIngestFixture{started: make(chan struct{}), release: make(chan struct{})}
	executor.SetNativeIngestor(fixture)
	first, _ := startNativeOverlapIngest(ctx, executor)
	waitNativeOverlapStarted(t, fixture)
	second, messages := startNativeOverlapIngest(ctx, executor)
	waitUntilWaiting(t, second, messages)
	if got := fixture.calls.Load(); got != 1 {
		t.Fatalf("provider calls while first scan runs = %d, want 1", got)
	}
	close(fixture.release)
	for _, done := range []<-chan ingestOutcome{first, second} {
		got := awaitOutcome(t, done, "native full scan")
		if got.err != nil || got.result == nil || got.result.ScanResult == nil || got.result.ScanResult.New != 1 {
			t.Fatalf("native full scan result = %+v, %v; want its completed scan", got.result, got.err)
		}
	}
	if got := fixture.calls.Load(); got != 2 {
		t.Fatalf("completed provider calls = %d, want 2", got)
	}
}

// A cancelled waiter must never enter the native consumer. CancelLibrary
// must reach the native waiting claim as well as the active one.
func TestNativeStorageOverlapWaitCancellation(t *testing.T) {
	for _, cancelLibrary := range []bool{false, true} {
		name := "waiting context"
		if cancelLibrary {
			name = "whole library"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			executor, _ := newNativeDispatchExecutor()
			fixture := &gatedNativeIngestFixture{started: make(chan struct{}), release: make(chan struct{})}
			executor.SetNativeIngestor(fixture)
			first, _ := startNativeOverlapIngest(ctx, executor)
			waitNativeOverlapStarted(t, fixture)
			waitCtx, cancelWait := context.WithCancel(ctx)
			defer cancelWait()
			second, messages := startNativeOverlapIngest(waitCtx, executor)
			waitUntilWaiting(t, second, messages)
			if cancelLibrary {
				if n := executor.CancelLibrary(42); n != 2 {
					t.Fatalf("cancelled %d native claims, want running and waiting", n)
				}
			} else {
				cancelWait()
			}
			if got := awaitOutcome(t, second, "cancelled native waiter"); !errors.Is(got.err, context.Canceled) || got.result != nil {
				t.Fatalf("cancelled native waiter = %+v, %v", got.result, got.err)
			}
			close(fixture.release)
			got := awaitOutcome(t, first, "first native scan")
			if cancelLibrary && !errors.Is(got.err, context.Canceled) {
				t.Fatalf("library cancellation missed native scan: %v", got.err)
			}
			if !cancelLibrary && (got.err != nil || got.result == nil || got.result.ScanResult == nil || got.result.ScanResult.New != 1) {
				t.Fatalf("canceling waiter affected first scan: %+v, %v", got.result, got.err)
			}
			if n := fixture.calls.Load(); n != 1 {
				t.Fatalf("cancelled waiter entered native consumer: %d calls", n)
			}
			executor.mu.Lock()
			defer executor.mu.Unlock()
			if len(executor.running) != 0 || len(executor.waiting) != 0 {
				t.Fatalf("native claims leaked: running=%d waiting=%d", len(executor.running), len(executor.waiting))
			}
		})
	}
}
