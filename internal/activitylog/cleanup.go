package activitylog

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/dblock"
)

// cleanupLockKey guards CleanupOnce so only one replica performs the
// activity-log retention pass per day; every replica's RunCleanup ticker
// fires independently, so without this lock N replicas would all prune (and
// drop/create partitions for) the same activity_log table concurrently.
var cleanupLockKey = dblock.Key("activitylog.cleanup")

// tryLockFunc overrides advisory-lock acquisition in tests. Nil in
// production, where CleanupOnce falls back to dblock.TryLock.
var tryLockFunc func(ctx context.Context, pool *pgxpool.Pool, key int64) (*dblock.Lock, bool, error)

const (
	keyRetentionDays    = "activitylog.retention_days"
	defaultRetentionStr = "90"
	defaultRetention    = 90
	cleanupBatchSize    = 10000
)

// SettingsStore is satisfied by *catalog.ServerSettingsRepo.
type SettingsStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

type PartitionManager interface {
	EnsureFuturePartitions(ctx context.Context) error
	DropExpiredPartitions(ctx context.Context, cutoff time.Time) ([]string, error)
	DeleteExpiredRowsFromDefault(ctx context.Context, cutoff time.Time) (int64, error)
}

// SeedDefaults writes default activity log settings if not already set.
func SeedDefaults(ctx context.Context, store SettingsStore) error {
	existing, err := store.Get(ctx, keyRetentionDays)
	if err != nil {
		return fmt.Errorf("seed activitylog defaults: %w", err)
	}
	if existing != "" {
		return nil
	}
	return store.Set(ctx, keyRetentionDays, defaultRetentionStr)
}

// RunCleanup starts a background goroutine that runs batched deletes daily.
// Blocks until ctx is canceled.
func RunCleanup(ctx context.Context, pool *pgxpool.Pool, store SettingsStore, pm PartitionManager) {
	// Run once at startup, then every 24 hours
	CleanupOnce(ctx, pool, store, pm)

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			CleanupOnce(ctx, pool, store, pm)
		}
	}
}

// CleanupOnce runs a single activity log retention pass. Guarded by a
// Postgres advisory lock (try-and-skip, not held for the run's duration
// beyond its own execution): a replica that loses the race logs and returns
// 0 rather than duplicating the prune/partition work.
func CleanupOnce(ctx context.Context, pool *pgxpool.Pool, store SettingsStore, pm PartitionManager) int64 {
	lock, locked, err := acquireCleanupLock(ctx, pool)
	if err != nil {
		slog.WarnContext(ctx, "activitylog cleanup advisory lock error, skipping run", "component", "activitylog", "error", err)
		return 0
	}
	if !locked {
		slog.DebugContext(ctx, "activitylog cleanup: another replica holds the lock, skipping run", "component", "activitylog")
		return 0
	}
	defer releaseCleanupLock(lock)

	days := defaultRetention
	if raw, err := store.Get(ctx, keyRetentionDays); err == nil && raw != "" {
		if d := parseInt(raw); d > 0 {
			days = d
		}
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if pm != nil {
		if err := pm.EnsureFuturePartitions(ctx); err != nil {
			slog.WarnContext(ctx, "activitylog ensure future partitions error", "component", "activitylog", "error", err)
		}

		partitionCleanupFailed := false
		totalDeleted := int64(0)
		if dropped, err := pm.DropExpiredPartitions(ctx, cutoff); err != nil {
			slog.WarnContext(ctx, "activitylog partition cleanup error", "component", "activitylog", "error", err)
			partitionCleanupFailed = true
		} else if len(dropped) > 0 {
			slog.InfoContext(ctx, "activitylog dropped expired partitions", "component", "activitylog", "partitions", dropped)
		}

		if deleted, err := pm.DeleteExpiredRowsFromDefault(ctx, cutoff); err != nil {
			slog.WarnContext(ctx, "activitylog default partition cleanup error", "component", "activitylog", "error", err)
			partitionCleanupFailed = true
		} else if deleted > 0 {
			totalDeleted += deleted
			slog.InfoContext(ctx, "activitylog default partition cleanup completed", "component", "activitylog", "deleted", deleted, "retention_days", days)
		}

		if !partitionCleanupFailed {
			return totalDeleted
		}
		slog.WarnContext(ctx, "activitylog partition cleanup degraded, falling back to row deletes", "component", "activitylog", "retention_days", days)
	}

	total := deleteExpiredRowsBefore(ctx, pool, cutoff)
	if total > 0 {
		slog.InfoContext(ctx, "activitylog cleanup completed", "component", "activitylog", "deleted", total, "retention_days", days)
	}
	return total
}

func acquireCleanupLock(ctx context.Context, pool *pgxpool.Pool) (*dblock.Lock, bool, error) {
	if tryLockFunc != nil {
		return tryLockFunc(ctx, pool, cleanupLockKey)
	}
	if pool == nil {
		return nil, false, fmt.Errorf("activitylog cleanup has no database pool")
	}
	return dblock.TryLock(ctx, pool, cleanupLockKey)
}

func releaseCleanupLock(lock *dblock.Lock) {
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lock.Unlock(unlockCtx); err != nil {
		slog.ErrorContext(unlockCtx, "activitylog cleanup: failed to release advisory lock", "component", "activitylog", "error", err)
	}
}

func deleteExpiredRowsBefore(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) int64 {
	total := int64(0)
	for {
		result, err := pool.Exec(ctx, `
			DELETE FROM activity_log
			WHERE id IN (
				SELECT id FROM activity_log
				WHERE timestamp < $1
				LIMIT $2
			)
			`, cutoff, cleanupBatchSize)
		if err != nil {
			slog.WarnContext(ctx, "activitylog cleanup error", "component", "activitylog", "error", err)
			return total
		}
		deleted := result.RowsAffected()
		total += deleted
		if deleted < int64(cleanupBatchSize) {
			break
		}
	}
	return total
}

func parseInt(s string) int {
	// Malformed input yields 0, which callers treat as "unset".
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}
