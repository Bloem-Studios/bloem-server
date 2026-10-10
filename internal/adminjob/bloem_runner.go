package adminjob

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
	"github.com/jackc/pgx/v5/pgxpool"
)

// libraryDeletionRepository keeps heartbeat, progress and the final claim-fenced
// result independent of bundle writers occupying the caller's pool. Prepare the
// connection before any destructive work and preserve the original pool hooks.
func (r *Runner) libraryDeletionRepository(ctx context.Context) (*Repository, error) {
	cfg := r.repo.pool.Config()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create library deletion metadata pool: %w", err)
	}
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := pool.Ping(connectCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect library deletion metadata pool: %w", err)
	}
	return &Repository{pool: pool, claim: r.repo.claim}, nil
}

// startLibraryDeletionHeartbeat returns a function that cancels and joins the loop
// before its execution-owned metadata pool closes. No heartbeat remains after
// finalization, and cancellation interrupts a pending database acquisition.
func (r *Runner) startLibraryDeletionHeartbeat(ctx context.Context, jobID string) func() {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		r.heartbeatLoop(heartbeatCtx, jobID, stop)
	}()
	return func() {
		close(stop)
		cancel()
		<-done
	}
}

const (
	libraryDeletionProgressTimeout = time.Second
	libraryDeletionProgressRetry   = 30 * time.Second
)

// libraryDeletionProgress stays synchronous and bounds both the database write
// and event publication. Database contention or delayed event publication must
// not add an unbounded wait to every catalog batch.
// After a timeout, back off and wait for pool capacity before trying again.
// Completion still records the durable result after the guard has been released.
func (r *Runner) libraryDeletionProgress(ctx context.Context, jobID string) func(int, int, string) {
	var retryAfter time.Time
	return func(current, total int, message string) {
		if ctx.Err() != nil {
			return
		}
		if !retryAfter.IsZero() {
			stats := r.repo.pool.Stat()
			if time.Now().Before(retryAfter) || stats.AcquiredConns() >= stats.MaxConns() {
				return
			}
		}
		progressCtx, cancel := context.WithTimeout(ctx, libraryDeletionProgressTimeout)
		defer cancel()
		defer func() {
			if progressCtx.Err() != nil {
				retryAfter = time.Now().Add(libraryDeletionProgressRetry)
			}
		}()
		if err := r.repo.UpdateProgress(progressCtx, jobID, current, total, message); err != nil {
			slog.Warn("admin jobs: failed to update delete progress", "job_id", jobID, "error", err)
			return
		}
		r.publishJobByID(progressCtx, notifications.TypeJobProgress, jobID)
	}
}

func verifyCatalogSeedDigest(data []byte, expected string) error {
	if expected == "" {
		return nil
	}
	want, err := hex.DecodeString(expected)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("catalog import source digest is invalid")
	}
	actual := sha256.Sum256(data)
	if subtle.ConstantTimeCompare(actual[:], want) != 1 {
		return fmt.Errorf("catalog import source digest mismatch")
	}
	return nil
}
