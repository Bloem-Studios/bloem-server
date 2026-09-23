package opslog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
	"github.com/jackc/pgx/v5/pgxpool"
)

// cleanupLockKey guards CleanupOnce so only one replica performs the
// operational-log retention pass per tick; every replica's RunCleanup
// ticker fires independently, so without this lock N replicas would all
// prune (and drop/create partitions for) the same operational_logs table
// concurrently.
var cleanupLockKey = pglock.Key("opslog.cleanup")

// tryLockFunc overrides advisory-lock acquisition in tests. Nil in
// production, where CleanupOnce falls back to pglock.TryAcquire.
var tryLockFunc func(ctx context.Context, pool *pgxpool.Pool, key int64) (*pglock.Lock, bool, error)

func acquireCleanupLock(ctx context.Context, pool *pgxpool.Pool) (*pglock.Lock, bool, error) {
	if tryLockFunc != nil {
		return tryLockFunc(ctx, pool, cleanupLockKey)
	}
	if pool == nil {
		return nil, false, fmt.Errorf("opslog cleanup has no database pool")
	}
	return pglock.TryAcquire(ctx, pool, cleanupLockKey)
}

func releaseCleanupLock(lock *pglock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Release(unlockCtx); err != nil {
		slog.ErrorContext(unlockCtx, "opslog cleanup: failed to release advisory lock", "component", "opslog", "error", err)
	}
}

// lockCleanupRun guards CleanupOnce with a Postgres advisory lock
// (try-and-skip, not held for the run's duration beyond its own execution):
// a replica that loses the race logs and CleanupOnce returns 0 rather than
// duplicating the prune/partition work.
func lockCleanupRun(ctx context.Context, pool *pgxpool.Pool) (func(), bool) {
	lock, locked, err := acquireCleanupLock(ctx, pool)
	if err != nil {
		slog.WarnContext(ctx, "opslog cleanup advisory lock error, skipping run", "component", "opslog", "error", err)
		return nil, false
	}
	if !locked {
		slog.DebugContext(ctx, "opslog cleanup: another replica holds the lock, skipping run", "component", "opslog")
		return nil, false
	}
	return func() { releaseCleanupLock(lock) }, true
}
