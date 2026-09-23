package recommendations

import (
	"context"
	"log/slog"
	"time"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
)

// Advisory-lock keys for the cron jobs below. On a single-replica deployment
// these locks are always uncontended and add one Acquire/Release round trip
// per scheduled run; on multiple replicas they ensure only one replica
// actually executes a given tick instead of every replica redundantly
// recomputing embeddings/taste-profiles/co-watch/recommendation caches.
var (
	embeddingsLockKey      = pglock.Key("recommendations.embeddings")
	tasteProfilesLockKey   = pglock.Key("recommendations.taste_profiles")
	cowatchLockKey         = pglock.Key("recommendations.cowatch")
	recommendationsLockKey = pglock.Key("recommendations.recommendations_cache")
)

// acquireJobLock tries to claim the given advisory-lock key on the engine's
// pool. Extracted as a var-backed method (rather than a package-level call)
// so tests can stub it to simulate a lock already held by another replica
// without needing a second real Postgres connection.
func (w *Worker) acquireJobLock(ctx context.Context, key int64) (*pglock.Lock, bool, error) {
	if w.tryLockFunc != nil {
		return w.tryLockFunc(ctx, key)
	}
	return pglock.TryAcquire(ctx, w.engine.pool, key)
}

func (w *Worker) releaseJobLock(lock *pglock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Release(unlockCtx); err != nil {
		slog.ErrorContext(unlockCtx, "recommendations: failed to release advisory lock", "error", err)
	}
}

// lockScheduledRun claims a cron job's advisory lock, logging and reporting
// false when the run must be skipped (lock error, or another replica holds
// it). The returned func releases the lock.
func (w *Worker) lockScheduledRun(ctx context.Context, key int64, job string) (func(), bool) {
	lock, locked, err := w.acquireJobLock(ctx, key)
	if err != nil {
		slog.ErrorContext(ctx, job+": advisory lock error, skipping run", "error", err)
		return nil, false
	}
	if !locked {
		slog.InfoContext(ctx, job+": another replica holds the lock, skipping scheduled run")
		return nil, false
	}
	return func() { w.releaseJobLock(lock) }, true
}
