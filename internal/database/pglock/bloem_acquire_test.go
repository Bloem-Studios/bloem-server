package pglock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	bloemAcquireTestKey   int64 = 0x70676C6F636B02
	bloemAcquireTestClass int32 = 0x70676C62
	bloemAcquireTestObj   int32 = 7
)

func singleConnPool(t *testing.T, base *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()
	cfg := base.Config().Copy()
	cfg.MaxConns = 1
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("single-connection pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func pairHeld(t *testing.T, pool *pgxpool.Pool, classID, objID int32) bool {
	t.Helper()
	var held bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND granted AND objsubid = 2
				AND classid::bigint = $1 AND objid::bigint = $2
		)`, int64(classID), int64(objID)).Scan(&held); err != nil {
		t.Fatalf("inspect pg_locks: %v", err)
	}
	return held
}

func waitForWaiter(t *testing.T, pool *pgxpool.Pool, key int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1 FROM pg_locks
				WHERE locktype = 'advisory' AND NOT granted
					AND ((classid::bigint << 32) | objid::bigint) = $1
			)`, key).Scan(&waiting); err != nil {
			t.Fatalf("inspect pg_locks: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("second Acquire never waited on the lock")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func backendPID(t *testing.T, pool *pgxpool.Pool) int32 {
	t.Helper()
	var pid int32
	if err := pool.QueryRow(context.Background(), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	return pid
}

func TestAcquireBlocksUntilHolderReleases(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := Acquire(ctx, pool, bloemAcquireTestKey)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	got := make(chan *Lock, 1)
	errs := make(chan error, 1)
	go func() {
		second, err := Acquire(ctx, pool, bloemAcquireTestKey)
		if err != nil {
			errs <- err
			return
		}
		got <- second
	}()
	waitForWaiter(t, pool, bloemAcquireTestKey)
	select {
	case <-got:
		t.Fatal("second Acquire returned while the lock was held")
	default:
	}
	if err := first.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	select {
	case second := <-got:
		if err := second.Release(ctx); err != nil {
			t.Fatalf("second Release: %v", err)
		}
	case err := <-errs:
		t.Fatalf("second Acquire: %v", err)
	case <-ctx.Done():
		t.Fatal("second Acquire did not proceed after Release")
	}
	if lockHeld(t, pool, bloemAcquireTestKey) {
		t.Fatal("lock still held after both releases")
	}
}

func TestAcquireCancelledWhileWaitingHoldsNothing(t *testing.T) {
	base := testPool(t)
	pool := singleConnPool(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	holder, err := Acquire(ctx, base, bloemAcquireTestKey)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	waitCtx, stop := context.WithCancel(ctx)
	errs := make(chan error, 1)
	go func() {
		lock, err := Acquire(waitCtx, pool, bloemAcquireTestKey)
		if err == nil {
			_ = lock.Release(ctx)
		}
		errs <- err
	}()
	waitForWaiter(t, base, bloemAcquireTestKey)
	stop()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Acquire error = %v, want context.Canceled", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled Acquire did not return")
	}
	if err := holder.Release(ctx); err != nil {
		t.Fatalf("holder Release: %v", err)
	}
	// Neither the discarded waiter session nor anything returned to the pool
	// may hold the lock once the holder is gone.
	deadline := time.Now().Add(5 * time.Second)
	for lockHeld(t, base, bloemAcquireTestKey) {
		if time.Now().After(deadline) {
			t.Fatal("cancelled waiter left the lock held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The single-connection pool must still be usable.
	again, err := Acquire(ctx, pool, bloemAcquireTestKey)
	if err != nil {
		t.Fatalf("Acquire after cancel: %v", err)
	}
	if err := again.Release(ctx); err != nil {
		t.Fatalf("Release after cancel: %v", err)
	}
}

// A failed unlock must never hand the still-locking session back to the pool.
// The single-connection pool makes reuse observable through the backend pid.
func TestAcquireReleaseFailureDiscardsConnection(t *testing.T) {
	base := testPool(t)
	pool := singleConnPool(t, base)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("unlock reports not held", func(t *testing.T) {
		lock, err := Acquire(ctx, pool, bloemAcquireTestKey)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		var pid int32
		if err := lock.Conn().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		var unlocked bool
		if err := lock.Conn().QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, bloemAcquireTestKey).Scan(&unlocked); err != nil || !unlocked {
			t.Fatalf("out of band unlock = (%v, %v)", unlocked, err)
		}
		if err := lock.Release(ctx); err == nil {
			t.Fatal("Release reported success for an unheld lock")
		}
		if next := backendPID(t, pool); next == pid {
			t.Fatal("connection with an unconfirmed unlock was returned to the pool")
		}
	})

	t.Run("backend terminated", func(t *testing.T) {
		lock, err := Acquire(ctx, pool, bloemAcquireTestKey)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		var pid int32
		if err := lock.Conn().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		var terminated bool
		if err := base.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
			t.Fatalf("pg_terminate_backend = (%v, %v)", terminated, err)
		}
		if err := lock.Release(ctx); err == nil {
			t.Fatal("Release reported success on a terminated backend")
		}
		if next := backendPID(t, pool); next == pid {
			t.Fatal("terminated connection was returned to the pool")
		}
		if lockHeld(t, base, bloemAcquireTestKey) {
			t.Fatal("lock still held after terminated backend")
		}
	})

	t.Run("pair unlock reports not held", func(t *testing.T) {
		lock, err := AcquirePair(ctx, pool, bloemAcquireTestClass, bloemAcquireTestObj)
		if err != nil {
			t.Fatalf("AcquirePair: %v", err)
		}
		if !pairHeld(t, base, bloemAcquireTestClass, bloemAcquireTestObj) {
			t.Fatal("AcquirePair did not take the two-int4 lock")
		}
		var pid int32
		if err := lock.Conn().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		var unlocked bool
		if err := lock.Conn().QueryRow(ctx, `SELECT pg_advisory_unlock($1::int4, $2::int4)`, bloemAcquireTestClass, bloemAcquireTestObj).Scan(&unlocked); err != nil || !unlocked {
			t.Fatalf("out of band unlock = (%v, %v)", unlocked, err)
		}
		if err := lock.Release(ctx); err == nil {
			t.Fatal("Release reported success for an unheld pair lock")
		}
		if err := lock.Release(ctx); err != nil {
			t.Fatalf("second Release: %v", err)
		}
		if next := backendPID(t, pool); next == pid {
			t.Fatal("connection with an unconfirmed pair unlock was returned to the pool")
		}
	})
}

func TestAcquirePairExcludesTransactionLockOnSamePair(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lock, err := AcquirePair(ctx, pool, bloemAcquireTestClass, bloemAcquireTestObj)
	if err != nil {
		t.Fatalf("AcquirePair: %v", err)
	}
	var got bool
	if err := pool.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1::int4, $2::int4)`, bloemAcquireTestClass, bloemAcquireTestObj).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("transaction lock on the same pair was granted while AcquirePair held it")
	}
	if err := lock.Release(ctx); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if pairHeld(t, pool, bloemAcquireTestClass, bloemAcquireTestObj) {
		t.Fatal("pair lock still held after Release")
	}
}

func TestAcquireNilPool(t *testing.T) {
	if _, err := Acquire(context.Background(), nil, bloemAcquireTestKey); !errors.Is(err, ErrNilPool) {
		t.Fatalf("Acquire(nil) error = %v", err)
	}
	if _, err := AcquirePair(context.Background(), nil, 1, 2); !errors.Is(err, ErrNilPool) {
		t.Fatalf("AcquirePair(nil) error = %v", err)
	}
}
