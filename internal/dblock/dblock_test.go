package dblock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func newSingleConnectionDBLockTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" && os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
		t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
	}
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}

	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database config: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("connect single-connection test database pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping single-connection test database pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func acquireTestLock(t *testing.T, pool *pgxpool.Pool, name string) *Lock {
	t.Helper()
	lock, acquired, err := TryLock(context.Background(), pool, Key(name))
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !acquired {
		t.Fatal("TryLock did not acquire an uncontended lock")
	}
	return lock
}

func requireConnectionRetired(t *testing.T, pool *pgxpool.Pool, retired *pgx.Conn, retiredPID uint32) {
	t.Helper()
	if !retired.PgConn().IsClosed() {
		t.Fatal("failed unlock connection remains open")
	}
	stats := pool.Stat()
	if stats.AcquiredConns() != 0 || stats.IdleConns() != 0 || stats.TotalConns() != 0 {
		t.Fatalf(
			"failed unlock connection remains accounted to pool: acquired=%d idle=%d total=%d",
			stats.AcquiredConns(), stats.IdleConns(), stats.TotalConns(),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	replacement, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire replacement connection: %v", err)
	}
	defer replacement.Release()
	if replacement.Conn() == retired {
		t.Fatal("pool reused the exact connection whose unlock failed")
	}
	if replacement.Conn().PgConn().PID() == retiredPID {
		t.Fatalf("pool reused backend PID %d after failed unlock", retiredPID)
	}
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

func TestLock_UnlockFalseRetiresConnection(t *testing.T) {
	pool := newSingleConnectionDBLockTestPool(t)
	lock := acquireTestLock(t, pool, fmt.Sprintf("dblock-test-unlock-false-%d", time.Now().UnixNano()))
	ctx := context.Background()
	retired := lock.conn.Conn()
	retiredPID := retired.PgConn().PID()

	var unlocked bool
	if err := lock.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, lock.key).Scan(&unlocked); err != nil {
		t.Fatalf("remove held advisory lock before Unlock: %v", err)
	}
	if !unlocked {
		t.Fatal("test setup did not remove the held advisory lock")
	}

	err := lock.Unlock(ctx)
	if err == nil || !strings.Contains(err.Error(), "was not held") {
		t.Fatalf("Unlock error = %v, want advisory-lock-not-held error", err)
	}
	requireConnectionRetired(t, pool, retired, retiredPID)
}

func TestLock_UnlockQueryErrorRetiresConnection(t *testing.T) {
	pool := newSingleConnectionDBLockTestPool(t)
	lock := acquireTestLock(t, pool, fmt.Sprintf("dblock-test-unlock-error-%d", time.Now().UnixNano()))
	retired := lock.conn.Conn()
	retiredPID := retired.PgConn().PID()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := lock.Unlock(canceledCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Unlock error = %v, want context.Canceled", err)
	}
	requireConnectionRetired(t, pool, retired, retiredPID)
}

func TestLock_UnlockSuccessReleasesConnection(t *testing.T) {
	pool := newSingleConnectionDBLockTestPool(t)
	lock := acquireTestLock(t, pool, fmt.Sprintf("dblock-test-unlock-success-%d", time.Now().UnixNano()))
	released := lock.conn.Conn()
	releasedPID := released.PgConn().PID()

	if err := lock.Unlock(context.Background()); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if released.PgConn().IsClosed() {
		t.Fatal("successful unlock closed the pooled connection")
	}
	stats := pool.Stat()
	if stats.AcquiredConns() != 0 || stats.IdleConns() != 1 || stats.TotalConns() != 1 {
		t.Fatalf(
			"successful unlock pool accounting: acquired=%d idle=%d total=%d, want 0/1/1",
			stats.AcquiredConns(), stats.IdleConns(), stats.TotalConns(),
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reacquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("reacquire released connection: %v", err)
	}
	defer reacquired.Release()
	if reacquired.Conn() != released || reacquired.Conn().PgConn().PID() != releasedPID {
		t.Fatalf(
			"successful unlock did not return the same connection: got PID %d, want %d",
			reacquired.Conn().PgConn().PID(), releasedPID,
		)
	}
}

func TestLock_UnlockRepeatedIsNoop(t *testing.T) {
	pool := newSingleConnectionDBLockTestPool(t)
	lock := acquireTestLock(t, pool, fmt.Sprintf("dblock-test-unlock-repeated-%d", time.Now().UnixNano()))

	if err := lock.Unlock(context.Background()); err != nil {
		t.Fatalf("first Unlock: %v", err)
	}
	if err := lock.Unlock(context.Background()); err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
}

func TestLock_UnlockConcurrentIsNoopAfterFirstDetach(t *testing.T) {
	pool := newSingleConnectionDBLockTestPool(t)
	lock := acquireTestLock(t, pool, fmt.Sprintf("dblock-test-unlock-concurrent-%d", time.Now().UnixNano()))

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					results <- fmt.Errorf("Unlock panicked: %v", recovered)
				}
			}()
			<-start
			results <- lock.Unlock(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Errorf("concurrent Unlock: %v", err)
		}
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Fatalf("acquired connections after concurrent Unlock = %d, want 0", got)
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
