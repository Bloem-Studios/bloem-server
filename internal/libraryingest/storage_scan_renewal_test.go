package libraryingest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// waitForLock stands in for a renewal blocked on the library lock a page
// transaction holds: it returns the driver's timeout once its attempt ends.
func waitForLock(ctx context.Context) error {
	<-ctx.Done()
	return fmt.Errorf("%w: storage scan authority: timeout: %w", resourcetenancy.ErrResourceUnavailable, ctx.Err())
}

func renewalJob(t *testing.T) (context.Context, context.CancelCauseFunc) {
	t.Helper()
	job, cancel := context.WithCancelCause(t.Context())
	t.Cleanup(func() { cancel(nil) })
	return job, cancel
}

func TestStorageRenewalRetriesAttemptsBlockedByPageWork(t *testing.T) {
	job, cancel := renewalJob(t)
	var attempts atomic.Int32
	renewed := make(chan struct{})
	stop, _ := startStorageRenewal(job, cancel, 10*time.Millisecond, time.Minute, func(ctx context.Context) error {
		n := attempts.Add(1)
		if n <= 3 {
			return waitForLock(ctx)
		}
		if n == 4 {
			close(renewed)
		}
		return nil
	})
	defer stop()
	select {
	case <-renewed:
	case <-job.Done():
		t.Fatalf("a renewal blocked by page work stopped the scan: %v", context.Cause(job))
	case <-time.After(10 * time.Second):
		t.Fatal("renewal was not retried")
	}
	stop()
	if job.Err() != nil {
		t.Fatalf("scan stopped: %v", context.Cause(job))
	}
}

func TestStorageRenewalPageConfirmationsKeepTimeoutsTolerable(t *testing.T) {
	job, cancel := renewalJob(t)
	const ttl = 80 * time.Millisecond
	stop, confirm := startStorageRenewal(job, cancel, 10*time.Millisecond, ttl, waitForLock)
	defer stop()
	// Pages keep committing, so every renewal waits behind one; the lease stays current.
	for deadline := time.Now().Add(5 * ttl); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		confirm()
		if job.Err() != nil {
			t.Fatalf("scan stopped while pages renewed the lease: %v", context.Cause(job))
		}
	}
	// Once nothing has renewed the lease within ttl, a timed-out renewal stops the scan.
	select {
	case <-job.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("renewal kept retrying after the lease lapsed")
	}
	if cause := context.Cause(job); !errors.Is(cause, context.DeadlineExceeded) {
		t.Fatalf("lapsed lease cause: %v", cause)
	}
}

func TestStorageRenewalFencesStopTheScan(t *testing.T) {
	for name, renew := range map[string]func(context.Context) error{
		"stale lease":       func(context.Context) error { return storagesource.ErrStaleLease },
		"revoked authority": func(context.Context) error { return resourcetenancy.ErrResourceHidden },
		"fence after wait": func(ctx context.Context) error {
			<-ctx.Done()
			return errors.Join(resourcetenancy.ErrResourceHidden, ctx.Err())
		},
		"stale after wait": func(ctx context.Context) error {
			<-ctx.Done()
			return errors.Join(storagesource.ErrStaleLease, ctx.Err())
		},
	} {
		t.Run(name, func(t *testing.T) {
			job, cancel := renewalJob(t)
			stop, _ := startStorageRenewal(job, cancel, 10*time.Millisecond, time.Minute, renew)
			defer stop()
			select {
			case <-job.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("fence did not stop the scan")
			}
			cause := context.Cause(job)
			if !errors.Is(cause, storagesource.ErrStaleLease) && !errors.Is(cause, resourcetenancy.ErrResourceHidden) {
				t.Fatalf("fence cause lost: %v", cause)
			}
		})
	}
}

func TestStorageRenewalStopSuppressesOnlyItsOwnCancellation(t *testing.T) {
	job, cancel := renewalJob(t)
	entered := make(chan struct{})
	stop, _ := startStorageRenewal(job, cancel, 10*time.Millisecond, time.Minute, func(ctx context.Context) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-ctx.Done()
		return ctx.Err()
	})
	<-entered
	stop()
	if job.Err() != nil {
		t.Fatalf("stopping renewal failed the scan: %v", context.Cause(job))
	}
}
