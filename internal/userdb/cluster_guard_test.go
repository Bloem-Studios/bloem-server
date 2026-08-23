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
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
		if _, err := pool.Exec(context.Background(), `DELETE FROM node_heartbeats WHERE node_id = $1`, nodeID); err != nil {
			t.Fatalf("clear heartbeat %s: %v", nodeID, err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM userdb_sqlite_owner`)
		for _, nodeID := range nodeIDs {
			_, _ = pool.Exec(ctx, `DELETE FROM node_heartbeats WHERE node_id = $1`, nodeID)
		}
	})
}
