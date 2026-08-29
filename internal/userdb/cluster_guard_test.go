package userdb_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/userdb"
	"github.com/Silo-Server/silo-server/internal/worker"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nonCooperativeContextHandler struct{}

func (nonCooperativeContextHandler) HandleCancel(context.Context) {}
func (nonCooperativeContextHandler) HandleUnwatchAfterCancel()    {}

func TestClusterSafeUserDBAllowsSharedPostgresWithoutAClaim(t *testing.T) {
	if err := userdb.EnforceClusterSafeBackend(context.Background(), nil, "postgres", "", ""); err != nil {
		t.Fatalf("EnforceClusterSafeBackend(postgres): %v", err)
	}
}

func TestClusterSafeUserDBFencesActiveAndForeignSQLiteNodes(t *testing.T) {
	pool := openClusterGuardPool(t)
	ownerNode := "sqlite-owner-" + uuid.NewString()
	foreignNode := "sqlite-foreign-" + uuid.NewString()
	cleanupClusterGuardRows(t, pool, ownerNode, foreignNode)

	if err := userdb.EnforceClusterSafeBackend(context.Background(), pool, "sqlite", ownerNode, "integrated"); err != nil {
		t.Fatalf("first SQLite claim: %v", err)
	}
	if err := userdb.EnforceClusterSafeBackend(context.Background(), pool, "sqlite", ownerNode, "integrated"); !errors.Is(err, userdb.ErrSQLiteNodeAlreadyActive) {
		t.Fatalf("second active SQLite claim error = %v, want ErrSQLiteNodeAlreadyActive", err)
	}

	if _, err := pool.Exec(context.Background(), `UPDATE node_heartbeats SET updated_at = NOW() - INTERVAL '2 minutes' WHERE node_id = $1`, ownerNode); err != nil {
		t.Fatalf("age stopped owner heartbeat: %v", err)
	}
	if err := userdb.EnforceClusterSafeBackend(context.Background(), pool, "sqlite", ownerNode, "integrated"); err != nil {
		t.Fatalf("same-node restart after stale heartbeat: %v", err)
	}

	if _, err := pool.Exec(context.Background(), `DELETE FROM node_heartbeats WHERE node_id = $1`, ownerNode); err != nil {
		t.Fatalf("remove restarted owner heartbeat: %v", err)
	}
	if err := userdb.EnforceClusterSafeBackend(context.Background(), pool, "sqlite", foreignNode, "integrated"); !errors.Is(err, userdb.ErrSQLiteOwnedByAnotherNode) {
		t.Fatalf("foreign SQLite claim error = %v, want ErrSQLiteOwnedByAnotherNode", err)
	}
}

func TestClusterSafeUserDBConcurrentSQLiteClaimsAdmitExactlyOneNode(t *testing.T) {
	pool := openClusterGuardPool(t)
	nodeA := "sqlite-race-a-" + uuid.NewString()
	nodeB := "sqlite-race-b-" + uuid.NewString()
	cleanupClusterGuardRows(t, pool, nodeA, nodeB)

	start := make(chan struct{})
	errorsByNode := make(chan error, 2)
	var workers sync.WaitGroup
	for _, nodeID := range []string{nodeA, nodeB} {
		workers.Add(1)
		go func(nodeID string) {
			defer workers.Done()
			<-start
			errorsByNode <- userdb.EnforceClusterSafeBackend(context.Background(), pool, "sqlite", nodeID, "api")
		}(nodeID)
	}
	close(start)
	workers.Wait()
	close(errorsByNode)

	successes := 0
	conflicts := 0
	for err := range errorsByNode {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, userdb.ErrSQLiteOwnedByAnotherNode):
			conflicts++
		default:
			t.Fatalf("concurrent claim error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent claims: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
}

func TestClusterSafeUserDBWorkerShutdownCleanupIsFinal(t *testing.T) {
	pool := openClusterGuardPool(t)
	nodeID := "sqlite-worker-shutdown-" + uuid.NewString()
	sessionID := "session-" + uuid.NewString()
	cleanupClusterGuardRows(t, pool, nodeID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := userdb.EnforceClusterSafeBackend(ctx, pool, "sqlite", nodeID, "integrated"); err != nil {
		t.Fatalf("claim SQLite owner: %v", err)
	}
	const nodeURL = "http://sqlite-heartbeat-owner"
	writer := worker.NewHeartbeatWriter(pool, nodeID, "integrated", nodeURL)
	writer.Start()
	waitForClusterGuardHeartbeatURL(t, ctx, pool, nodeID, nodeURL)

	reconcilePool := openGatedReconcilerPool(t)
	gate, err := reconcilePool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire reconciliation gate: %v", err)
	}
	defer func() {
		if gate != nil {
			gate.Release()
		}
	}()

	snapshotCaptured := make(chan struct{})
	var captureOnce sync.Once
	captures := 0
	now := time.Now().UTC()
	reconciler := worker.NewReconciler(reconcilePool, nodeID, func() []worker.SessionSync {
		captures++
		captureOnce.Do(func() { close(snapshotCaptured) })
		return []worker.SessionSync{{
			SessionID:     sessionID,
			UserID:        1,
			ReportingNode: nodeID,
			StartedAt:     now,
			UpdatedAt:     now,
		}}
	})
	reconcileResult := make(chan error, 1)
	go func() { reconcileResult <- reconciler.SyncNow(context.Background()) }()
	waitForClusterGuardSignal(t, ctx, snapshotCaptured, "reconciliation snapshot")
	if err := reconciler.SyncNow(ctx); err != nil {
		t.Fatalf("queue reconciliation follow-up: %v", err)
	}

	shutdownStarted := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		close(shutdownStarted)
		if err := reconciler.StopAndWait(ctx); err != nil {
			shutdownResult <- err
			return
		}
		if err := writer.StopAndWait(ctx); err != nil {
			shutdownResult <- err
			return
		}
		shutdownResult <- writer.CleanupSelf(ctx)
	}()
	waitForClusterGuardSignal(t, ctx, shutdownStarted, "worker shutdown")
	select {
	case err := <-shutdownResult:
		t.Fatalf("worker shutdown passed the held reconciliation gate before cleanup: %v", err)
	default:
	}

	gate.Release()
	gate = nil
	if err := waitForClusterGuardResult(t, ctx, reconcileResult, "queued reconciliation"); err != nil {
		t.Fatalf("queued reconciliation: %v", err)
	}
	if err := waitForClusterGuardResult(t, ctx, shutdownResult, "ordered worker shutdown"); err != nil {
		t.Fatalf("ordered worker shutdown: %v", err)
	}
	if captures != 1 {
		t.Fatalf("reconciliation snapshots after stop = %d, want queued follow-up suppressed", captures)
	}

	if err := reconciler.SyncNow(ctx); err != nil {
		t.Fatalf("sync after cleanup: %v", err)
	}
	reconciler.Start()
	if err := reconciler.StopAndWait(ctx); err != nil {
		t.Fatalf("repeat reconciler stop after cleanup: %v", err)
	}
	writer.Start()
	if err := writer.StopAndWait(ctx); err != nil {
		t.Fatalf("repeat stop after cleanup: %v", err)
	}
	if sessions := clusterGuardSessionCount(t, ctx, pool, nodeID); sessions != 0 {
		t.Fatalf("owner sessions after shutdown cleanup = %d, want 0", sessions)
	}
	if heartbeats := clusterGuardHeartbeatCount(t, ctx, pool, nodeID); heartbeats != 0 {
		t.Fatalf("owner heartbeats after shutdown cleanup = %d, want 0", heartbeats)
	}
}

func TestClusterSafeUserDBHeartbeatShutdownTimeoutLeavesHeartbeatForExpiry(t *testing.T) {
	pool := openClusterGuardPool(t)
	nodeID := "sqlite-heartbeat-timeout-" + uuid.NewString()
	cleanupClusterGuardRows(t, pool, nodeID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := userdb.EnforceClusterSafeBackend(ctx, pool, "sqlite", nodeID, "integrated"); err != nil {
		t.Fatalf("claim SQLite owner: %v", err)
	}
	lockTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin heartbeat row lock: %v", err)
	}
	defer func() { _ = lockTx.Rollback(context.Background()) }()
	if _, err := lockTx.Exec(ctx, `SELECT node_id FROM node_heartbeats WHERE node_id = $1 FOR UPDATE`, nodeID); err != nil {
		t.Fatalf("lock owner heartbeat row: %v", err)
	}

	applicationName := "heartbeat-timeout-" + uuid.NewString()
	heartbeatPool := openNonCooperativeHeartbeatPool(t, applicationName)
	writer := worker.NewHeartbeatWriter(heartbeatPool, nodeID, "integrated", "http://sqlite-heartbeat-timeout")
	writer.Start()
	defer writer.Stop()
	waitForClusterGuardHeartbeatQuery(t, ctx, pool, applicationName)

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	if err := writer.StopAndWait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopAndWait error = %v, want context.DeadlineExceeded", err)
	}
	if heartbeats := clusterGuardHeartbeatCount(t, ctx, pool, nodeID); heartbeats != 1 {
		t.Fatalf("owner heartbeats after timed-out shutdown = %d, want 1 left for expiry", heartbeats)
	}

	if err := lockTx.Rollback(ctx); err != nil {
		t.Fatalf("release heartbeat row lock: %v", err)
	}
	if err := writer.StopAndWait(ctx); err != nil {
		t.Fatalf("join heartbeat writer after releasing row lock: %v", err)
	}
	if err := writer.CleanupSelf(ctx); err != nil {
		t.Fatalf("cleanup joined heartbeat writer: %v", err)
	}
	if heartbeats := clusterGuardHeartbeatCount(t, ctx, pool, nodeID); heartbeats != 0 {
		t.Fatalf("owner heartbeats after joined cleanup = %d, want 0", heartbeats)
	}
}

func TestClusterSafeUserDBReconcilerShutdownTimeoutLeavesSharedRowsForExpiry(t *testing.T) {
	pool := openClusterGuardPool(t)
	nodeID := "sqlite-reconciler-timeout-" + uuid.NewString()
	sessionID := "session-" + uuid.NewString()
	cleanupClusterGuardRows(t, pool, nodeID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := userdb.EnforceClusterSafeBackend(ctx, pool, "sqlite", nodeID, "integrated"); err != nil {
		t.Fatalf("claim SQLite owner: %v", err)
	}
	now := time.Now().UTC()
	if _, err := pool.Exec(ctx, `
		INSERT INTO playback_sessions_sync
			(session_id, user_id, media_file_id, reporting_node, started_at, updated_at, last_sync_at)
		VALUES ($1, $2, $3, $4, $5, $5, $5)
	`, sessionID, 1, 0, nodeID, now); err != nil {
		t.Fatalf("seed owner session: %v", err)
	}

	reconcilePool := openGatedReconcilerPool(t)
	gate, err := reconcilePool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire reconciliation gate: %v", err)
	}
	defer func() {
		if gate != nil {
			gate.Release()
		}
	}()

	snapshotCaptured := make(chan struct{})
	var captureOnce sync.Once
	reconciler := worker.NewReconciler(reconcilePool, nodeID, func() []worker.SessionSync {
		captureOnce.Do(func() { close(snapshotCaptured) })
		return []worker.SessionSync{{
			SessionID:     sessionID,
			UserID:        1,
			ReportingNode: nodeID,
			StartedAt:     now,
			UpdatedAt:     now,
		}}
	})
	reconcileResult := make(chan error, 1)
	go func() { reconcileResult <- reconciler.SyncNow(context.Background()) }()
	waitForClusterGuardSignal(t, ctx, snapshotCaptured, "reconciliation snapshot")
	select {
	case err := <-reconcileResult:
		t.Fatalf("reconciliation passed the held pool connection before shutdown: %v", err)
	default:
	}

	reconciler.Stop()
	heartbeatWriter := worker.NewHeartbeatWriter(pool, nodeID, "integrated", "http://sqlite-reconciler-timeout")
	heartbeatWriter.Stop()

	deadlineCtx, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	reconcileErr := reconciler.StopAndWait(deadlineCtx)
	if !errors.Is(reconcileErr, context.DeadlineExceeded) {
		t.Fatalf("reconciler StopAndWait error = %v, want context.DeadlineExceeded", reconcileErr)
	}
	heartbeatErr := heartbeatWriter.StopAndWait(deadlineCtx)
	if heartbeatErr != nil {
		t.Fatalf("stopped heartbeat writer join: %v", heartbeatErr)
	}
	if reconcileErr == nil && heartbeatErr == nil {
		if err := heartbeatWriter.CleanupSelf(deadlineCtx); err != nil {
			t.Fatalf("unexpected shared ownership cleanup: %v", err)
		}
	}

	if sessions := clusterGuardSessionCount(t, ctx, pool, nodeID); sessions != 1 {
		t.Fatalf("owner sessions after timed-out reconciler shutdown = %d, want 1 left for expiry", sessions)
	}
	if heartbeats := clusterGuardHeartbeatCount(t, ctx, pool, nodeID); heartbeats != 1 {
		t.Fatalf("owner heartbeats after timed-out reconciler shutdown = %d, want 1 left for expiry", heartbeats)
	}

	gate.Release()
	gate = nil
	if err := waitForClusterGuardResult(t, ctx, reconcileResult, "queued reconciliation"); err != nil {
		t.Fatalf("queued reconciliation: %v", err)
	}
	if err := reconciler.StopAndWait(ctx); err != nil {
		t.Fatalf("join reconciler after releasing gate: %v", err)
	}
	if err := heartbeatWriter.CleanupSelf(ctx); err != nil {
		t.Fatalf("cleanup joined workers: %v", err)
	}
	if sessions := clusterGuardSessionCount(t, ctx, pool, nodeID); sessions != 0 {
		t.Fatalf("owner sessions after joined cleanup = %d, want 0", sessions)
	}
	if heartbeats := clusterGuardHeartbeatCount(t, ctx, pool, nodeID); heartbeats != 0 {
		t.Fatalf("owner heartbeats after joined cleanup = %d, want 0", heartbeats)
	}
}

func waitForClusterGuardHeartbeatURL(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID, wantURL string) {
	t.Helper()
	for {
		var nodeURL *string
		err := pool.QueryRow(ctx, `SELECT node_url FROM node_heartbeats WHERE node_id = $1`, nodeID).Scan(&nodeURL)
		if err == nil && nodeURL != nil && *nodeURL == wantURL {
			return
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("read owner heartbeat: %v", err)
		}
		if ctx.Err() != nil {
			t.Fatalf("waiting for owner heartbeat URL %q: %v", wantURL, ctx.Err())
		}
	}
}

func waitForClusterGuardHeartbeatQuery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, applicationName string) {
	t.Helper()
	for {
		var active bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE application_name = $1
				  AND state = 'active'
				  AND query LIKE '%INSERT INTO node_heartbeats%'
			)`, applicationName).Scan(&active); err != nil {
			t.Fatalf("inspect blocked heartbeat query: %v", err)
		}
		if active {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("waiting for blocked heartbeat query: %v", ctx.Err())
		}
	}
}

func waitForClusterGuardSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
	}
}

func waitForClusterGuardResult(t *testing.T, ctx context.Context, result <-chan error, label string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
		return ctx.Err()
	}
}

func clusterGuardSessionCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) int {
	t.Helper()
	var sessions int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM playback_sessions_sync WHERE reporting_node = $1`, nodeID).Scan(&sessions); err != nil {
		t.Fatalf("count owner sessions: %v", err)
	}
	return sessions
}

func clusterGuardHeartbeatCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, nodeID string) int {
	t.Helper()
	var heartbeats int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM node_heartbeats WHERE node_id = $1`, nodeID).Scan(&heartbeats); err != nil {
		t.Fatalf("count owner heartbeats: %v", err)
	}
	return heartbeats
}

func openNonCooperativeHeartbeatPool(t *testing.T, applicationName string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse heartbeat test pool config: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	config.ConnConfig.BuildContextWatcherHandler = func(*pgconn.PgConn) ctxwatch.Handler {
		return nonCooperativeContextHandler{}
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open heartbeat test pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping heartbeat test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openGatedReconcilerPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse reconciler test pool config: %v", err)
	}
	config.MaxConns = 1
	config.MinConns = 0
	config.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open reconciler test pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping reconciler test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func openClusterGuardPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.RunMigrations(context.Background(), pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return pool
}

func cleanupClusterGuardRows(t *testing.T, pool *pgxpool.Pool, nodeIDs ...string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `DELETE FROM userdb_sqlite_owner`); err != nil {
		t.Fatalf("clear SQLite owner: %v", err)
	}
	for _, nodeID := range nodeIDs {
		if _, err := pool.Exec(context.Background(), `DELETE FROM playback_sessions_sync WHERE reporting_node = $1`, nodeID); err != nil {
			t.Fatalf("clear sessions %s: %v", nodeID, err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM node_heartbeats WHERE node_id = $1`, nodeID); err != nil {
			t.Fatalf("clear heartbeat %s: %v", nodeID, err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM userdb_sqlite_owner`)
		for _, nodeID := range nodeIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM playback_sessions_sync WHERE reporting_node = $1`, nodeID)
			_, _ = pool.Exec(ctx, `DELETE FROM node_heartbeats WHERE node_id = $1`, nodeID)
		}
	})
}
