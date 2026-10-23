package audiobooks

// Fixtures Bloem's tests share with Silo tests that upstream has since
// removed. They are copied unchanged so the Silo files stay deleted.

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newClaimTestPool(t *testing.T) *pgxpool.Pool {
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

// seedAudiobook inserts one audiobook row and returns its content ID. A
// non-empty poster is the default because that is the production shape: the
// scanner extracts embedded cover art from the file long before enrichment
// runs.
func seedAudiobook(t *testing.T, pool *pgxpool.Pool, label, poster string, refreshed bool) string {
	t.Helper()
	ctx := context.Background()
	contentID := fmt.Sprintf("audiobook-claim-%s-%d", label, time.Now().UnixNano())

	var refreshedAt any
	if refreshed {
		refreshedAt = time.Now()
	}

	// claimBatch only selects items with a library membership, and
	// media_item_libraries references media_folders, so the fixture creates
	// both. Deleting the folder cascades the membership; deleting the item
	// removes its state row.
	var folderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name)
		VALUES ('audiobooks', $1)
		RETURNING id
	`, fmt.Sprintf("Claim Fixture %d", time.Now().UnixNano())).Scan(&folderID); err != nil {
		t.Fatalf("seed media folder for %s: %v", contentID, err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (
			content_id, type, title, genres, poster_path, last_refreshed,
			refresh_failures, episode_metadata_incomplete
		) VALUES ($1, 'audiobook', 'Claim Fixture', '{}'::text[], $2, $3, 0, FALSE)
	`, contentID, poster, refreshedAt); err != nil {
		t.Fatalf("seed audiobook %s: %v", contentID, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_item_libraries (content_id, media_folder_id, first_seen_at)
		VALUES ($1, $2, now())
	`, contentID, folderID); err != nil {
		t.Fatalf("seed library membership for %s: %v", contentID, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM media_item_provider_ids WHERE content_id = $1`, contentID)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM media_items WHERE content_id = $1`, contentID)
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM media_folders WHERE id = $1`, folderID)
	})
	return contentID
}
