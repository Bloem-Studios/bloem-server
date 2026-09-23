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
