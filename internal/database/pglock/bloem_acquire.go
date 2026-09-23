package pglock

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Bloem-owned blocking acquisition. Upstream's pglock only offers TryAcquire;
// Bloem has call sites that must wait for the lock (idempotent apply receipts,
// per-user quota admission, initial setup admission). They previously
// hand-rolled pg_advisory_lock/unlock pairs, and one of them returned the
// connection to the pool after a failed unlock. Everything here funnels
// through the same "destroy the connection when the unlock is not confirmed"
// rule as [Lock.Release].

// ErrNilPool is returned by the blocking acquirers when no pool is
// configured. Unlike TryAcquire, a blocking caller must not mistake "no
// database" for "lock held".
var ErrNilPool = errors.New("pglock: nil pool")

// Acquire blocks until session-level advisory lock key is held, or ctx ends.
//
// The wait runs on a dedicated pool connection. If the wait fails for any
// reason (including ctx cancellation racing the grant), the connection is
// hijacked out of the pool and closed, so a lock the server may have granted
// just as the client gave up can never be returned to the pool.
//
// The caller owns the returned Lock and must call Release exactly once.
func Acquire(ctx context.Context, pool *pgxpool.Pool, key int64) (*Lock, error) {
	conn, err := blockingLock(ctx, pool, `SELECT pg_advisory_lock($1)`, fmt.Sprintf("advisory lock %d", key), key)
	if err != nil {
		return nil, err
	}
	return &Lock{conn: conn, key: key}, nil
}

// PairLock is a held session-level advisory lock in PostgreSQL's two-int4 key
// space (pg_advisory_lock(int4, int4)). That space is distinct from the bigint
// space used by [Lock], so call sites that already lock a (classid, objid)
// pair — possibly as a transaction lock elsewhere — must keep using it for
// mixed-version deployments to keep excluding each other.
type PairLock struct {
	conn    *pgxpool.Conn
	classID int32
	objID   int32
}

// AcquirePair is [Acquire] for the two-int4 key space.
func AcquirePair(ctx context.Context, pool *pgxpool.Pool, classID, objID int32) (*PairLock, error) {
	conn, err := blockingLock(ctx, pool, `SELECT pg_advisory_lock($1::int4, $2::int4)`, fmt.Sprintf("advisory lock (%d,%d)", classID, objID), classID, objID)
	if err != nil {
		return nil, err
	}
	return &PairLock{conn: conn, classID: classID, objID: objID}, nil
}

// Conn exposes the connection holding the lock.
func (l *PairLock) Conn() *pgxpool.Conn {
	if l == nil {
		return nil
	}
	return l.conn
}

// Release unlocks and returns the connection to the pool, or destroys the
// connection when the unlock cannot be confirmed. Idempotent and nil-safe.
func (l *PairLock) Release(ctx context.Context) error {
	if l == nil || l.conn == nil {
		return nil
	}
	conn := l.conn
	l.conn = nil

	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	var unlocked bool
	err := conn.QueryRow(releaseCtx, `SELECT pg_advisory_unlock($1::int4, $2::int4)`, l.classID, l.objID).Scan(&unlocked)
	if err == nil && unlocked {
		conn.Release()
		return nil
	}
	discard(ctx, conn)
	if err != nil {
		return fmt.Errorf("releasing advisory lock (%d,%d): %w", l.classID, l.objID, err)
	}
	return fmt.Errorf("releasing advisory lock (%d,%d): lock was not held", l.classID, l.objID)
}

func blockingLock(ctx context.Context, pool *pgxpool.Pool, sql, desc string, args ...any) (*pgxpool.Conn, error) {
	if pool == nil {
		return nil, ErrNilPool
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring connection for %s: %w", desc, err)
	}
	if _, err := conn.Exec(ctx, sql, args...); err != nil {
		// The grant may have raced the cancellation: never pool this session.
		discard(ctx, conn)
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			return nil, fmt.Errorf("acquiring %s: %w: %w", desc, ctxErr, err)
		}
		return nil, fmt.Errorf("acquiring %s: %w", desc, err)
	}
	return conn, nil
}

// discard hijacks conn out of the pool and closes it; closing the session
// releases every advisory lock it held.
func discard(ctx context.Context, conn *pgxpool.Conn) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	_ = conn.Hijack().Close(closeCtx)
}
