package scanner

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/models"
)

func newDeadRootTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
func scopedFolderPaths(folder *models.MediaFolder, paths []string) *models.MediaFolder {
	if folder == nil {
		return nil
	}
	clone := *folder
	clone.Paths = paths
	return &clone
}
