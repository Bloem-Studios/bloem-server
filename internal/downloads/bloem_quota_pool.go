package downloads

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
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
	// Keep the existing two-int key space: session and transaction locks on
	// this pair conflict, including with older nodes during rolling upgrades.
	// pglock discards the session whenever acquisition or unlock is uncertain.
	if userID < math.MinInt32 || userID > math.MaxInt32 {
		return fmt.Errorf("acquire download quota lock: user id %d out of int4 range", userID)
	}
	lock, err := pglock.AcquirePair(ctx, pool, downloadQuotaLockClassID, int32(userID))
	if err != nil {
		return fmt.Errorf("acquire download quota lock: %w", err)
	}
	scope := &bloemQuotaScope{pool: pool, conn: lock.Conn(), userID: userID}
	scope.active.Store(true)
	defer func() {
		scope.active.Store(false)
		if releaseErr := lock.Release(ctx); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release download quota lock: %w", releaseErr))
		}
	}()
	return fn(context.WithValue(ctx, bloemQuotaScopeKey{}, scope))
}
