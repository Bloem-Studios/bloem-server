package downloads

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBloemQuotaLockUncertainSessionsAreDiscarded(t *testing.T) {
	fixture := seedManagedFixture(t)
	cfg := fixture.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := NewRepository(pool)

	t.Run("cancelled-acquisition", func(t *testing.T) {
		ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
		defer stop()
		var waitingPID int
		if err := pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&waitingPID); err != nil {
			t.Fatal(err)
		}
		// An old node's transaction lock must exclude the new session lock.
		holder, err := fixture.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = holder.Rollback(cleanup)
		}()
		if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1,$2)`, downloadQuotaLockClassID, fixture.userID); err != nil {
			t.Fatal(err)
		}
		request, cancel := context.WithCancel(ctx)
		defer cancel()
		done := make(chan error, 1)
		called := false
		go func() {
			done <- repo.WithUserQuotaLock(request, fixture.userID, func(context.Context) error {
				called = true
				return nil
			})
		}()
		// Cancel only after PostgreSQL actually reports the blocked acquisition,
		// rather than hoping a sleep puts the goroutine in the intended state.
		for {
			var waiting bool
			if err := fixture.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND NOT granted)`, waitingPID).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("quota acquisition never blocked on the old-node lock")
			case <-time.After(10 * time.Millisecond):
			}
		}
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) || called {
				t.Fatalf("cancelled acquisition: callback=%t err=%v", called, err)
			}
		case <-ctx.Done():
			t.Fatal("quota acquisition ignored cancellation")
		}
		if pool.Stat().TotalConns() != 0 {
			t.Fatal("uncertain acquisition returned its session to the pool")
		}
	})

	t.Run("missing-unlock", func(t *testing.T) {
		err := repo.WithUserQuotaLock(t.Context(), fixture.userID, func(ctx context.Context) error {
			// Model uncertain/broken ownership without touching another session.
			_, err := repo.pool.Exec(ctx, `SELECT pg_advisory_unlock_all()`)
			return err
		})
		if err == nil || pool.Stat().TotalConns() != 0 {
			t.Fatalf("unconfirmed unlock reused the session: err=%v conns=%d", err, pool.Stat().TotalConns())
		}
	})
	check, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	if err := NewRepository(fixture.pool).WithUserQuotaLock(check, fixture.userID, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("failed acquisition/unlock retained a cross-node lock: %v", err)
	}
}
