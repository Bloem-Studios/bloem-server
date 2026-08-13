package catalog

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deleteCatalogTestMediaFolders removes the compatibility entitlements that
// are materialized automatically for new folders before deleting the folders.
func deleteCatalogTestMediaFolders(t *testing.T, ctx context.Context, pool *pgxpool.Pool, folderIDs ...int) {
	t.Helper()
	if len(folderIDs) == 0 {
		return
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id = ANY($1)`, folderIDs); err != nil {
		t.Errorf("cleanup media folder entitlements: %v", err)
		return
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_folders WHERE id = ANY($1)`, folderIDs); err != nil {
		t.Errorf("cleanup media folders: %v", err)
	}
}
