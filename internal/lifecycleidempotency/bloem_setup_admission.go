package lifecycleidempotency

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/database/pglock"
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
	// pglock never returns a possibly locked session to the pool: not when
	// cancellation races acquisition, and not when the unlock is unconfirmed.
	lock, err := pglock.Acquire(ctx, s.pool, lockID)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire initial setup admission: %w", err)
	}
	release := func() { _ = lock.Release(context.Background()) }
	tx, err := lock.Conn().BeginTx(ctx, options)
	if err != nil {
		release()
		return nil, nil, err
	}
	return tx, release, nil
}
