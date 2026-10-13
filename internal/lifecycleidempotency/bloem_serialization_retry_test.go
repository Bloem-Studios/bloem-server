package lifecycleidempotency

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Account identity writes bump the managed-tracking clock row, so a lifecycle
// writer whose repeatable-read snapshot predates another publication fails
// with SQLSTATE 40001. The store reruns the whole attempt on a fresh snapshot.
func TestLifecycleTransactionRerunsSerializationFailures(t *testing.T) {
	ctx := context.Background()
	pool := newLifecycleStoreDatabase(t)
	store := NewPostgresStore(pool)
	clock := func(t *testing.T) int64 {
		t.Helper()
		var revision int64
		if err := pool.QueryRow(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton`).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}

	t.Run("a write conflict on a published row reruns on a fresh snapshot", func(t *testing.T) {
		before := clock(t)
		attempts := 0
		err := store.InTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
			attempts++
			if _, err := tx.Exec(ctx, `SELECT revision FROM bloem_managed_clock WHERE singleton`); err != nil {
				return err
			}
			if attempts == 1 {
				// Another publisher commits after this attempt pinned its snapshot.
				if _, err := pool.Exec(ctx, `UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton`); err != nil {
					return err
				}
			}
			_, err := tx.Exec(ctx, `UPDATE bloem_managed_clock SET revision=revision+1 WHERE singleton`)
			return err
		})
		if err != nil {
			t.Fatalf("InTransaction() error = %v, want the rerun to commit", err)
		}
		if attempts != 2 {
			t.Fatalf("attempts = %d, want 2", attempts)
		}
		if after := clock(t); after != before+2 {
			t.Fatalf("clock revision = %d, want %d: the concurrent and the rerun publication", after, before+2)
		}
	})

	t.Run("other failures are returned without a rerun", func(t *testing.T) {
		injected := errors.New("injected lifecycle failure")
		attempts := 0
		err := store.InTransaction(ctx, func(context.Context, pgx.Tx) error {
			attempts++
			return injected
		})
		if !errors.Is(err, injected) || attempts != 1 {
			t.Fatalf("InTransaction() error = %v after %d attempts, want the injected failure once", err, attempts)
		}
	})

	t.Run("persistent serialization failures stop at the bound", func(t *testing.T) {
		attempts := 0
		err := store.InTransaction(ctx, func(context.Context, pgx.Tx) error {
			attempts++
			return &pgconn.PgError{Code: "40001"}
		})
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); !ok || pgErr.Code != "40001" {
			t.Fatalf("InTransaction() error = %v, want the last serialization failure", err)
		}
		if attempts != serializationAttempts {
			t.Fatalf("attempts = %d, want %d", attempts, serializationAttempts)
		}
	})

	t.Run("a cancelled request is not rerun", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		attempts := 0
		err := store.InTransaction(cancelled, func(context.Context, pgx.Tx) error {
			attempts++
			cancel()
			return &pgconn.PgError{Code: "40001"}
		})
		if err == nil || attempts != 1 {
			t.Fatalf("InTransaction() error = %v after %d attempts, want one failed attempt", err, attempts)
		}
	})
}
