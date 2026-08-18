package dblock

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newDBLockTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
		t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
	}
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Two concurrent TryLock calls against the same key from two separate
// connections must result in exactly one caller acquiring the lock — this
// is the core guarantee dblock exists to provide across replicas.
func TestTryLock_ExactlyOneOfTwoConcurrentAttemptsSucceeds(t *testing.T) {
	pool := newDBLockTestPool(t)
	ctx := context.Background()
	key := Key(fmt.Sprintf("dblock-test-concurrent-%d", time.Now().UnixNano()))

	lockA, gotA, err := TryLock(ctx, pool, key)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if !gotA {
		t.Fatal("first TryLock should have acquired the uncontended lock")
	}
	defer func() {
		if err := lockA.Unlock(ctx); err != nil {
			t.Errorf("unlock A: %v", err)
		}
	}()

	// Second attempt on the same key, from a different connection, must lose.
	lockB, gotB, err := TryLock(ctx, pool, key)
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if gotB {
		t.Fatal("second TryLock should NOT have acquired an already-held lock")
	}
	if lockB != nil {
		t.Fatal("a failed TryLock must return a nil *Lock")
	}
}

// After Unlock releases a key, a subsequent TryLock on that same key must
// succeed — this is the "skip this run, try again next tick" behavior the
// six background jobs depend on.
func TestTryLock_ReacquirableAfterUnlock(t *testing.T) {
	pool := newDBLockTestPool(t)
	ctx := context.Background()
	key := Key(fmt.Sprintf("dblock-test-reacquire-%d", time.Now().UnixNano()))

	lock1, got1, err := TryLock(ctx, pool, key)
	if err != nil || !got1 {
		t.Fatalf("first TryLock: got=%v err=%v", got1, err)
	}
	if err := lock1.Unlock(ctx); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	lock2, got2, err := TryLock(ctx, pool, key)
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if !got2 {
		t.Fatal("TryLock should reacquire a key released by Unlock")
	}
	if err := lock2.Unlock(ctx); err != nil {
		t.Errorf("unlock2: %v", err)
	}
}

// Distinct keys never contend with each other.
func TestTryLock_DistinctKeysDoNotContend(t *testing.T) {
	pool := newDBLockTestPool(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	keyA := Key(fmt.Sprintf("dblock-test-distinct-a-%d", suffix))
	keyB := Key(fmt.Sprintf("dblock-test-distinct-b-%d", suffix))

	lockA, gotA, err := TryLock(ctx, pool, keyA)
	if err != nil || !gotA {
		t.Fatalf("lock A: got=%v err=%v", gotA, err)
	}
	defer func() { _ = lockA.Unlock(ctx) }()

	lockB, gotB, err := TryLock(ctx, pool, keyB)
	if err != nil || !gotB {
		t.Fatalf("lock B: got=%v err=%v", gotB, err)
	}
	defer func() { _ = lockB.Unlock(ctx) }()
}

// Unlock on a nil *Lock (the "TryLock returned false" case) is a no-op, so
// callers can unconditionally defer it.
func TestLock_UnlockNilIsNoop(t *testing.T) {
	var l *Lock
	if err := l.Unlock(context.Background()); err != nil {
		t.Fatalf("unlock nil lock: %v", err)
	}
}

// Key is deterministic for a given name and differs across names.
func TestKey_DeterministicAndDistinct(t *testing.T) {
	first := Key("job-a")
	second := Key("job-a")
	if first != second {
		t.Fatal("Key must be deterministic for the same input")
	}
	if Key("job-a") == Key("job-b") {
		t.Fatal("Key should differ for different inputs (collision, however unlikely)")
	}
}
