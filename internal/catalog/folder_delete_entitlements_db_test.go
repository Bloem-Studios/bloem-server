package catalog

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestDeleteWithStatsReleasesOrganizationEntitlements is a regression guard for
// a bug that made library deletion impossible.
//
// Bloem auto-entitles the default organization to every new media folder
// (trigger bloem_entitle_default_organization_media_folder, migration
// 20260813090000), and organization_entitlements references media_folders
// ON DELETE RESTRICT. DeleteWithStats deleted the folder row without releasing
// that entitlement, so every library deletion failed with
// organization_entitlements_media_folder_fkey. Nothing covered the deletion
// path end to end, so it stayed broken.
func TestDeleteWithStatsReleasesOrganizationEntitlements(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()

	var folderID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO media_folders (name, type) VALUES ('delete-entitlement-guard', 'movies') RETURNING id`,
	).Scan(&folderID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM organization_entitlements WHERE media_folder_id = $1`, folderID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM media_folders WHERE id = $1`, folderID)
	})

	// The trigger must have produced the entitlement this test exists to clear;
	// without it the test would pass even if the fix were reverted.
	var before int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM organization_entitlements WHERE media_folder_id = $1`, folderID,
	).Scan(&before); err != nil {
		t.Fatalf("count entitlements: %v", err)
	}
	if before == 0 {
		t.Fatal("no entitlement was auto-created; this guard cannot prove anything")
	}

	repo := NewFolderRepository(pool)
	if _, err := repo.DeleteWithStats(ctx, folderID, nil); err != nil {
		t.Fatalf("DeleteWithStats: %v", err)
	}

	var folders, entitlements int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_folders WHERE id = $1`, folderID).Scan(&folders); err != nil {
		t.Fatalf("count folders: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_entitlements WHERE media_folder_id = $1`, folderID).Scan(&entitlements); err != nil {
		t.Fatalf("count entitlements after: %v", err)
	}
	if folders != 0 {
		t.Errorf("folder row survived deletion")
	}
	if entitlements != 0 {
		t.Errorf("entitlements survived deletion: %d", entitlements)
	}
}
