package lifecycleidempotency

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type initialSetupAdmissionKey struct{}

// WithInitialSetupAdmission asks the PostgreSQL store to acquire the server's
// setup lock before beginning its repeatable-read transaction. Acquiring only
// a transaction lock is too late: the SELECT that waits for it already pins a
// snapshot, so a losing caller can still observe the pre-setup empty database.
// Only setup handlers supply this server-owned marker; no request field selects
// the lock. Other lifecycle operations keep their existing snapshot semantics.
func WithInitialSetupAdmission(ctx context.Context, lockID int64) context.Context {
	return context.WithValue(ctx, initialSetupAdmissionKey{}, lockID)
}

func (s *PostgresStore) beginTransaction(ctx context.Context) (pgx.Tx, func(), error) {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead}
	lockID, setup := ctx.Value(initialSetupAdmissionKey{}).(int64)
	if !setup {
		tx, err := s.pool.BeginTx(ctx, options)
		return tx, func() {}, err
	}

	// The lock and transaction share one pool connection. Acquiring a second
	// connection after locking could exhaust the pool under competing callers.
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	discard := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanupCtx)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		// Cancellation may race acquisition. Never return a possibly locked
		// session to the pool, even if the client did not observe success.
		discard()
		conn.Release()
		return nil, nil, fmt.Errorf("acquire initial setup admission: %w", err)
	}
	release := func() {
		defer conn.Release()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(cleanupCtx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked); err != nil || !unlocked {
			discard()
		}
	}
	tx, err := conn.BeginTx(ctx, options)
	if err != nil {
		release()
		return nil, nil, err
	}
	return tx, release, nil
}
