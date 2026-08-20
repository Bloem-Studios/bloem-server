package sessioninvalidation

import (
	"context"
	"errors"
	"testing"
	"time"
)

type contextKey string

func TestRunDetachesCancellationAndPreservesValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey("trace"), "trace-123"))
	cancel()
	wantErr := errors.New("persistence failed")

	err := Run(ctx, func(callbackCtx context.Context) error {
		if err := callbackCtx.Err(); err != nil {
			t.Fatalf("callback context inherited cancellation: %v", err)
		}
		if got := callbackCtx.Value(contextKey("trace")); got != "trace-123" {
			t.Fatalf("trace value = %v, want trace-123", got)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
}

func TestRunWithTimeoutBoundsCallback(t *testing.T) {
	started := time.Now()
	err := runWithTimeout(context.Background(), 10*time.Millisecond, func(callbackCtx context.Context) error {
		<-callbackCtx.Done()
		return callbackCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runWithTimeout error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("runWithTimeout took %s, want a bounded callback", elapsed)
	}
}
