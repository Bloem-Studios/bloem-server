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

// purgeStaleHeartbeats retires heartbeat rows older than the cleanup window,
// naming each node and instance so the delete fence admits it.
//
// A heartbeat may only be deleted by a session that names the exact node and
// instance it retires, so this cannot be one blind bulk delete: the sweeper
// reads the stale rows first and retires them one at a time, declaring each.
// That is the point of the fence -- a sweep must not be able to drop a live
// node's row by accident.
func (c *SessionCleaner) purgeStaleHeartbeats(ctx context.Context) error {
	rows, err := c.pool.Query(ctx, `
		SELECT node_id, instance_id
		FROM node_heartbeats
		WHERE updated_at < NOW() - make_interval(secs => $1::double precision)
	`, nodeHeartbeatCleanup.Seconds())
	if err != nil {
		return fmt.Errorf("listing stale heartbeats: %w", err)
	}
	type staleHeartbeat struct {
		nodeID     string
		instanceID *string
	}
	var stale []staleHeartbeat
	for rows.Next() {
		var entry staleHeartbeat
		if err := rows.Scan(&entry.nodeID, &entry.instanceID); err != nil {
			rows.Close()
			return fmt.Errorf("scanning stale heartbeat: %w", err)
		}
		stale = append(stale, entry)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("listing stale heartbeats: %w", err)
	}

	for _, entry := range stale {
		if entry.instanceID == nil {
			// A row from before nodes declared an instance cannot be named, so the
			// fence will not admit it. Leave it rather than fail the whole sweep;
			// it is inert, and a restart of that node replaces it.
			continue
		}
		if _, err := c.pool.Exec(ctx, `
			DELETE FROM node_heartbeats
			WHERE node_id = $1
			  AND set_config('bloem.heartbeat_cleanup_writer', 'v1', true) IS NOT NULL
			  AND set_config('bloem.heartbeat_cleanup_node_id', $1, true) IS NOT NULL
			  AND set_config('bloem.heartbeat_cleanup_instance_id', $2, true) IS NOT NULL
		`, entry.nodeID, *entry.instanceID); err != nil {
			return fmt.Errorf("retiring stale heartbeat for node %s: %w", entry.nodeID, err)
		}
	}
	return nil
}

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
