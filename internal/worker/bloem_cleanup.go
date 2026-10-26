package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// sessionCleanupLockKey guards SessionCleaner.CleanStale. Unlike
// Reconciler.tick (in reconciler.go), which syncs each replica's own
// node-scoped session rows and therefore MUST run on every replica,
// CleanStale purges globally-stale rows (dead-node sessions, expired
// heartbeats, and the hourly abandoned-audiobook-session sweep) that are
// not scoped to the running replica at all — any replica can perform this
// cleanup, so having all of them run it on every 15s tick is pure
// redundant work, not a correctness requirement.
var sessionCleanupLockKey = pglock.Key("worker.session_cleanup")

func (c *SessionCleaner) acquireCleanupLock(ctx context.Context) (*pglock.Lock, bool, error) {
	if c.tryLockFunc != nil {
		return c.tryLockFunc(ctx, sessionCleanupLockKey)
	}
	if c.pool == nil {
		return nil, false, fmt.Errorf("session cleaner has no database pool")
	}
	return pglock.TryAcquire(ctx, c.pool, sessionCleanupLockKey)
}

func (c *SessionCleaner) releaseCleanupLock(lock *pglock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Release(unlockCtx); err != nil {
		slog.ErrorContext(unlockCtx, "session cleanup: failed to release advisory lock", "error", err)
	}
}

// bloemTryLockFunc overrides advisory-lock acquisition in tests. Nil in
// production, where CleanStale falls back to pglock.TryAcquire.
type bloemTryLockFunc func(ctx context.Context, key int64) (*pglock.Lock, bool, error)

// lockCleanupRun guards CleanStale with a Postgres advisory lock so only one
// replica sweeps per tick. A nil release means CleanStale returns (0, err).
func (c *SessionCleaner) lockCleanupRun(ctx context.Context) (func(), error) {
	lock, locked, err := c.acquireCleanupLock(ctx)
	if err != nil {
		return nil, fmt.Errorf("session cleanup: advisory lock: %w", err)
	}
	if !locked {
		slog.DebugContext(ctx, "session cleanup: another replica holds the lock, skipping run")
		return nil, nil
	}
	return func() { c.releaseCleanupLock(lock) }, nil
}
