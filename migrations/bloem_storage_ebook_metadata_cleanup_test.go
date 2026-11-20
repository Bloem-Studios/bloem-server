package migrations

import "testing"

const (
	beforeStorageEbookMetadataCleanup = 20261120003125
	storageEbookMetadataCleanup       = 20261120120000
)

// TestStorageEbookMetadataCleanup: a storage book's joined subjects become
// separate genres without BISAC codes and its impossible date is cleared; a
// locked field, a library-folder book and a clean book stay as they are.
func TestStorageEbookMetadataCleanup(t *testing.T) {
	db, provider := newTestDBMigratedTo(t, beforeStorageEbookMetadataCleanup)
	exec(t, db,
		`DELETE FROM server_settings WHERE key = 'catalog.search.provider'`,
		`INSERT INTO server_settings (key, value) VALUES ('catalog.search.provider', 'meilisearch')`,
		`INSERT INTO media_folders (id, type, name) VALUES (91101, 'ebooks', 'Books')`,
		`INSERT INTO media_items (content_id, type, title, year, release_date, genres, locked_fields) VALUES
			('joined', 'ebook', 'Joined', 2922, '2922-11-21', '{"Historical Romance;Historical fiction;Novels;FIC042030","novels"}', '{}'),
			('locked', 'ebook', 'Locked', 2922, '2922-01-01', '{"A;B"}', '{2,13}'),
			('local', 'ebook', 'Local', 2922, NULL, '{"A;B"}', '{}'),
			('clean', 'ebook', 'Clean', 2021, '2021-05-01', '{"Fantasy"}', '{}')`,
		`INSERT INTO media_files (content_id, media_folder_id, file_path, file_size)
			SELECT c, 91101, p, 1
			FROM (VALUES ('joined', 'bloem-storage:joined'), ('locked', 'bloem-storage:locked'),
				('local', '/books/local.epub'), ('clean', 'bloem-storage:clean')) AS v(c, p)`,
	)
	if _, err := provider.UpTo(t.Context(), storageEbookMetadataCleanup); err != nil {
		t.Fatal(err)
	}

	assertCount(t, db, `SELECT count(*) FROM media_items WHERE content_id = 'joined'
		AND genres = '{"Historical Romance","Historical fiction",Novels}' AND year = 0 AND release_date IS NULL`, 1)
	assertCount(t, db, `SELECT count(*) FROM media_items WHERE content_id = 'locked'
		AND genres = '{"A;B"}' AND year = 2922`, 1)
	assertCount(t, db, `SELECT count(*) FROM media_items WHERE content_id = 'local'
		AND genres = '{"A;B"}' AND year = 2922`, 1)
	assertCount(t, db, `SELECT count(*) FROM media_items WHERE content_id = 'clean'
		AND genres = '{Fantasy}' AND year = 2021`, 1)
	// The cleaned book is reindexed once for its genres and once for its date;
	// nothing else is queued.
	assertCount(t, db, `SELECT count(*) FROM catalog_search_index_events
		WHERE provider = 'meilisearch' AND action = 'upsert' AND content_id = 'joined'`, 2)
	assertCount(t, db, `SELECT count(*) FROM catalog_search_index_events WHERE content_id <> 'joined'`, 0)
}
