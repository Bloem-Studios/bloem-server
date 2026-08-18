// Package dblock provides PostgreSQL advisory-lock based coordination for
// background jobs that must run on at most one server replica at a time.
//
// This codebase runs several `robfig/cron`-scheduled and ticker-driven
// background jobs (recommendation embeddings, collection sync, room/session
// cleanup sweeps, ...) that are started unconditionally in every process.
// A single in-process sync.Mutex only prevents re-entrancy within one
// process; on N replicas in a Kubernetes deployment, all N would still run
// the same scheduled work redundantly. dblock closes that gap with a
// Postgres advisory lock: each replica calls TryLock at the start of a
// scheduled run and only proceeds if it wins the lock, so the job still
// runs exactly once per tick across the whole fleet.
//
// Pattern: try-and-skip, not hold-for-process-lifetime. Advisory locks are
// connection-scoped (a session-level pg_advisory_lock is held by whichever
// connection took it, not by a transaction), so a lock that were held for
// the entire process lifetime would pin one pool connection per job for as
// long as the server runs — wasteful, and fragile if that connection ever
// drops without releasing the lock. Instead, each job acquires the lock
// with pg_try_advisory_lock at the start of its run, does its work, and
// releases it with pg_advisory_unlock when done. A replica that loses the
// race simply logs and skips that run; the next scheduled tick tries again.
package dblock

import (
	"context"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Lock is a held PostgreSQL session-level advisory lock, pinned to the
// pool connection that acquired it. Callers must release it via Unlock
// (typically deferred) once the guarded work completes, so the connection
// returns to the pool.
type Lock struct {
	conn *pgxpool.Conn
	key  int64
}

// TryLock attempts to acquire the session-level advisory lock identified by
// key using pg_try_advisory_lock. It reserves a dedicated connection from
// pool for the lifetime of the lock, so callers should hold it only for the
// duration of one job run and release it promptly via Unlock.
//
// Returns (nil, false, nil) — not an error — when the lock is already held
// by another session (typically another replica running the same job).
// Callers should treat that as "skip this run", log it, and return.
func TryLock(ctx context.Context, pool *pgxpool.Pool, key int64) (*Lock, bool, error) {
	if pool == nil {
		return nil, false, fmt.Errorf("dblock: nil pool")
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("dblock: acquire connection: %w", err)
	}

	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("dblock: pg_try_advisory_lock(%d): %w", key, err)
	}
	if !locked {
		conn.Release()
		return nil, false, nil
	}

	return &Lock{conn: conn, key: key}, true, nil
}

// Unlock releases the advisory lock with pg_advisory_unlock and returns the
// underlying connection to the pool. Safe to call on a nil *Lock (no-op),
// so callers can unconditionally `defer lock.Unlock(ctx)` after a TryLock
// that may have returned a nil lock.
func (l *Lock) Unlock(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	defer l.conn.Release()

	var unlocked bool
	if err := l.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, l.key).Scan(&unlocked); err != nil {
		return fmt.Errorf("dblock: pg_advisory_unlock(%d): %w", l.key, err)
	}
	if !unlocked {
		return fmt.Errorf("dblock: advisory lock %d was not held", l.key)
	}
	return nil
}

// Key derives a stable int64 advisory-lock key from a human-readable job
// name via FNV-1a, so call sites identify their lock by a descriptive
// string constant (e.g. "recommendations.embeddings") instead of a
// hand-maintained numeric registry that can silently collide as jobs are
// added over time. A hash collision between two of this codebase's small,
// fixed set of job names is astronomically unlikely, and even if it
// happened the failure mode is harmless: the two jobs would occasionally
// skip a run against each other instead of running concurrently.
func Key(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	return int64(h.Sum64())
}
