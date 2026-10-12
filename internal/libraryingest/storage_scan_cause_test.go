package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/storagesource"
)

func TestNativeScanCausePreservesTerminalClassification(t *testing.T) {
	for _, mode := range []string{"renewal", "joined-renewal", "process", "parent-cancel", "parent-deadline"} {
		t.Run(mode, func(t *testing.T) {
			parent, stop := context.WithCancel(context.Background())
			defer stop()
			if mode == "parent-deadline" {
				var cancel context.CancelFunc
				parent, cancel = context.WithDeadline(parent, time.Unix(1, 0))
				defer cancel()
			}
			job, cancel := context.WithCancelCause(parent)
			defer cancel(nil)
			want := storagesource.ErrStaleLease
			switch mode {
			case "process":
				want = errors.New("process unavailable")
			case "parent-cancel":
				stop()
				want = context.Canceled
			case "parent-deadline":
				want = context.DeadlineExceeded
			}
			cause := want
			if mode == "joined-renewal" {
				cause = fmt.Errorf("renewal wrapped: %w", errors.Join(want, context.Canceled))
			}
			cancel(cause)
			err := normalizeNativeScanError(parent, job, fmt.Errorf("provider wait: %w", errors.Join(context.Canceled, errors.New("I/O stopped"))))
			if !errors.Is(err, want) {
				t.Fatalf("lost cause: %v", err)
			}
			if parent.Err() == nil && errors.Is(err, context.Canceled) {
				t.Fatalf("internal failure classified as cancellation: %v", err)
			}
		})
	}
}
