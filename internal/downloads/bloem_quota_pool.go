package downloads

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bloemQuotaScopeKey struct{}

type bloemQuotaScope struct {
	pool   *pgxpool.Pool
	conn   *pgxpool.Conn
	userID int
	active atomic.Bool
}

// The callback and its repository calls share the connection holding admission.
// Waiters may fill the pool, but the owner never needs a second connection to
// make progress. Operations retain their existing commit/savepoint behavior;
// only quota admission uses a session lock. Never retain the scoped context or
// use it concurrently: a PostgreSQL connection has one synchronous borrower.
// The active fence also rejects accidental use after the callback returns.
type bloemQuotaPool struct{ *pgxpool.Pool }

func (p *bloemQuotaPool) scoped(ctx context.Context) (*pgxpool.Conn, error) {
	scope, _ := ctx.Value(bloemQuotaScopeKey{}).(*bloemQuotaScope)
	if scope == nil || scope.pool != p.Pool {
		return nil, nil
	}
	if !scope.active.Load() {
		return nil, errors.New("download quota scope has ended")
	}
	return scope.conn, nil
}

func (p *bloemQuotaPool) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	conn, err := p.scoped(ctx)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	if conn != nil {
		return conn.Exec(ctx, sql, args...)
	}
	return p.Pool.Exec(ctx, sql, args...)
}

func (p *bloemQuotaPool) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	conn, err := p.scoped(ctx)
	if err != nil {
		return nil, err
	}
	if conn != nil {
		return conn.Query(ctx, sql, args...)
	}
	return p.Pool.Query(ctx, sql, args...)
}

type bloemQuotaErrorRow struct{ err error }

func (r bloemQuotaErrorRow) Scan(...any) error { return r.err }

func (p *bloemQuotaPool) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	conn, err := p.scoped(ctx)
	if err != nil {
		return bloemQuotaErrorRow{err}
	}
	if conn != nil {
		return conn.QueryRow(ctx, sql, args...)
	}
	return p.Pool.QueryRow(ctx, sql, args...)
}

func (p *bloemQuotaPool) Begin(ctx context.Context) (pgx.Tx, error) {
	conn, err := p.scoped(ctx)
	if err != nil {
		return nil, err
	}
	if conn != nil {
		return conn.Begin(ctx)
	}
	return p.Pool.Begin(ctx)
}

func withBloemDownloadQuotaLock(ctx context.Context, pool *pgxpool.Pool, userID int, fn func(context.Context) error) (err error) {
	if scope, _ := ctx.Value(bloemQuotaScopeKey{}).(*bloemQuotaScope); scope != nil && scope.pool == pool {
		if !scope.active.Load() || scope.userID != userID {
			return errors.New("invalid nested download quota scope")
		}
		return fn(ctx)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire download quota connection: %w", err)
	}
	// Keep the existing two-int key space: session and transaction locks on
	// this pair conflict, including with older nodes during rolling upgrades.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1, $2)`, downloadQuotaLockClassID, userID); err != nil {
		discardBloemQuotaConnection(ctx, conn)
		return fmt.Errorf("acquire download quota lock: %w", err)
	}
	scope := &bloemQuotaScope{pool: pool, conn: conn, userID: userID}
	scope.active.Store(true)
	defer func() {
		scope.active.Store(false)
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRow(cleanup, `SELECT pg_advisory_unlock($1, $2)`, downloadQuotaLockClassID, userID).Scan(&unlocked)
		if unlockErr != nil || !unlocked {
			discardBloemQuotaConnection(ctx, conn)
			if unlockErr == nil {
				unlockErr = errors.New("download quota lock was not held")
			}
			err = errors.Join(err, fmt.Errorf("release download quota lock: %w", unlockErr))
			return
		}
		conn.Release()
	}()
	return fn(context.WithValue(ctx, bloemQuotaScopeKey{}, scope))
}

func discardBloemQuotaConnection(ctx context.Context, conn *pgxpool.Conn) {
	// Acquisition/unlock uncertainty must never return a potentially locked
	// session to the pool. Closing the hijacked connection releases every lock.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = conn.Hijack().Close(cleanup)
}
