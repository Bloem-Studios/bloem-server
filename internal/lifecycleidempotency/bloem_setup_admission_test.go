package lifecycleidempotency

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBloemSetupAdmissionIsolationAndConnectionCleanup(t *testing.T) {
	base := newLifecycleStoreDatabase(t)
	cfg := base.Config().Copy()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store := NewPostgresStore(pool)
	const lockID int64 = 4242420001
	ctx := WithInitialSetupAdmission(t.Context(), lockID)

	assertReleased := func(t *testing.T) {
		t.Helper()
		conn, err := base.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		// A discarded connection's server-side cleanup can finish after Close
		// returns locally. Wait for the observable release, not a fixed sleep.
		bounded, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(bounded, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
			t.Fatalf("setup admission lock was not released: %v", err)
		}
		if _, err := conn.Exec(t.Context(), `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
			t.Fatal(err)
		}
	}
	assertUsable := func(t *testing.T) {
		t.Helper()
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := store.InTransaction(bounded, func(ctx context.Context, tx pgx.Tx) error {
			var isolation string
			if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
				return err
			}
			if isolation != "repeatable read" {
				t.Errorf("isolation = %q, want repeatable read", isolation)
			}
			return nil
		}); err != nil {
			t.Fatalf("single-connection pool is unusable: %v", err)
		}
		assertReleased(t)
	}

	t.Run("commit", assertUsable)
	t.Run("rollback", func(t *testing.T) {
		injected := errors.New("setup callback failed")
		if err := store.InTransaction(ctx, func(context.Context, pgx.Tx) error { return injected }); !errors.Is(err, injected) {
			t.Fatalf("callback error = %v", err)
		}
		assertReleased(t)
		assertUsable(t)
	})
	t.Run("cancel after admission", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		err := store.InTransaction(cancelCtx, func(ctx context.Context, _ pgx.Tx) error {
			cancel()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled transaction error = %v", err)
		}
		assertReleased(t)
		assertUsable(t)
	})
	t.Run("cancel waiting for admission", func(t *testing.T) {
		held, err := base.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer held.Release()
		if _, err := held.Exec(t.Context(), `SELECT pg_advisory_lock($1)`, lockID); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = held.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID) }()
		waitCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- store.InTransaction(waitCtx, func(context.Context, pgx.Tx) error {
				t.Error("cancelled waiter reached mutation callback")
				return nil
			})
		}()
		deadline, stop := context.WithTimeout(t.Context(), 5*time.Second)
		defer stop()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting int
			if err := base.QueryRow(deadline, `SELECT count(*) FROM pg_locks
WHERE locktype='advisory' AND NOT granted AND classid=$1 AND objid=$2
AND database=(SELECT oid FROM pg_database WHERE datname=current_database())`, lockID>>32, lockID&0xffffffff).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting == 1 {
				break
			}
			select {
			case <-ticker.C:
			case <-deadline.Done():
				t.Fatal("setup caller did not wait for admission")
			}
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled admission error = %v", err)
			}
		case <-deadline.Done():
			t.Fatal("cancelled admission did not return")
		}
		if _, err := held.Exec(t.Context(), `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
			t.Fatal(err)
		}
		assertReleased(t)
		assertUsable(t)
	})
}
