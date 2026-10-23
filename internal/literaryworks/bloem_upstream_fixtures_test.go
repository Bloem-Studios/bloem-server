package literaryworks

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedLiteraryFolder(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO media_folders (type, name, enabled)
		VALUES ('books', 'Literary Works Test', true)
		RETURNING id
	`).Scan(&id); err != nil {
		t.Fatalf("seed media folder: %v", err)
	}
	return id
}

func seedLiteraryMediaItem(t *testing.T, pool *pgxpool.Pool, contentID, mediaType, title string, folderID int) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (content_id, type, title, genres)
		VALUES ($1, $2, $3, '{}'::text[])
	`, contentID, mediaType, title); err != nil {
		t.Fatalf("seed media item %s: %v", contentID, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_item_libraries (content_id, media_folder_id)
		VALUES ($1, $2)
	`, contentID, folderID); err != nil {
		t.Fatalf("seed media item library %s: %v", contentID, err)
	}
}
