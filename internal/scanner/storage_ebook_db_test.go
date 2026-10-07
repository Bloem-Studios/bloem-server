package scanner

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5/pgxpool"
)

func storageEbookEntry(id, format string, meta *storagev1.EbookMetadata, revision string) *storagev1.Entry {
	return &storagev1.Entry{Id: format + "/" + id, Name: id + "." + format, LogicalPath: format + "/" + id + "." + format,
		Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 1024, ModifiedUnixNano: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).UnixNano(),
		Revision: revision, Ebook: meta}
}

func storageEbookFixture(t *testing.T) (*pgxpool.Pool, *Scanner, *models.MediaFolder, storagesource.Location) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sources := storagesource.NewRepository(pool)
	source, err := sources.CreateSource(ctx, storagesource.SourceConfig{PluginID: "fixture", ProviderSourceID: fmt.Sprintf("books-%d", time.Now().UnixNano()), RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	folder := &models.MediaFolder{Type: "ebooks", Name: "Storage publish test", Enabled: true}
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('ebooks',$1) RETURNING id`, folder.Name).Scan(&folder.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	location, err := sources.AddLocationTx(ctx, tx, source.Key, folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id IN (SELECT content_id FROM media_item_libraries WHERE media_folder_id=$1)`, folder.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=$1`, folder.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, folder.ID)
		_, _ = pool.Exec(ctx, `DELETE FROM bloem_storage_sources WHERE key=$1`, source.Key)
	})
	return pool, NewScanner(NewFileRepository(pool), "", nil, 1, false, 0), folder, location
}

func publishStorageEbooks(t *testing.T, pool *pgxpool.Pool, s *Scanner, folder *models.MediaFolder, location storagesource.Location, entries ...*storagev1.Entry) StoragePublishResult {
	t.Helper()
	books := make([]StorageEbook, 0, len(entries))
	for _, e := range entries {
		book, ok := StorageEbookFromEntry(e)
		if !ok {
			t.Fatalf("entry %s carries no metadata", e.Id)
		}
		books = append(books, book)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := s.PublishStorageEbooksTx(t.Context(), tx, folder, location, 1, books)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPublishStorageEbooksFromProviderMetadata(t *testing.T) {
	pool, s, folder, location := storageEbookFixture(t)
	ctx := t.Context()
	suffix := fmt.Sprint(time.Now().UnixNano())
	author := "Storage Author " + suffix
	dune := &storagev1.EbookMetadata{Title: "Dune", Authors: []string{author, "Second " + suffix}, Description: "Spice.", Publisher: "Chilton",
		PublishedDate: "1965-08-01", Language: "en", Isbn: "9780441013593", Series: "Dune", SeriesIndex: "1", Genres: []string{"Science Fiction"},
		PageCount: 412, CoverEntryId: "cover/dune", CoverRevision: "c1", CoverThumbhash: "thumbhash-dune"}
	other := &storagev1.EbookMetadata{Title: "Untitled Notes " + suffix, Authors: []string{author}}
	result := publishStorageEbooks(t, pool, s, folder, location,
		storageEbookEntry("dune", "epub", dune, "sha256:a"),
		storageEbookEntry("dune", "pdf", dune, "sha256:b"),
		storageEbookEntry("notes", "epub", other, "sha256:c"))
	if result != (StoragePublishResult{New: 3}) {
		t.Fatalf("result = %+v", result)
	}

	var items, files, refs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
		(SELECT count(*) FROM media_files WHERE media_folder_id=$1),
		(SELECT count(*) FROM bloem_storage_file_refs WHERE location_id=$2)`, folder.ID, location.ID).Scan(&items, &files, &refs); err != nil {
		t.Fatal(err)
	}
	// The EPUB and PDF of one ISBN are two files of one item.
	if items != 2 || files != 3 || refs != 3 {
		t.Fatalf("items=%d files=%d refs=%d", items, files, refs)
	}
	duneID := storageEbookContentID(t, pool, folder.ID, "dune")
	var title, status, thumbhash, overview string
	var year int
	if err := pool.QueryRow(ctx, `SELECT title,status,COALESCE(poster_thumbhash,''),overview,year FROM media_items WHERE content_id=$1`, duneID).Scan(&title, &status, &thumbhash, &overview, &year); err != nil {
		t.Fatal(err)
	}
	if title != "Dune" || status != "matched" || thumbhash != "thumbhash-dune" || overview != "Spice." || year != 1965 {
		t.Fatalf("item = %q %q %q %q %d", title, status, thumbhash, overview, year)
	}
	var credits, seriesRows, isbns int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM item_people WHERE content_id=$1 AND kind=$2),
		(SELECT count(*) FROM ebook_series WHERE content_id=$1 AND series_name='Dune' AND series_index=1),
		(SELECT count(*) FROM media_item_provider_ids WHERE content_id=$1 AND provider='isbn' AND provider_id='9780441013593')`,
		duneID, models.PersonKindAuthor).Scan(&credits, &seriesRows, &isbns); err != nil {
		t.Fatal(err)
	}
	if credits != 2 || seriesRows != 1 || isbns != 1 {
		t.Fatalf("credits=%d series=%d isbns=%d", credits, seriesRows, isbns)
	}
	var coverEntry, coverRevision string
	if err := pool.QueryRow(ctx, `SELECT cover_entry_id, cover_revision FROM bloem_storage_file_refs WHERE location_id=$1 AND entry_id='epub/dune'`, location.ID).Scan(&coverEntry, &coverRevision); err != nil || coverEntry != "cover/dune" || coverRevision != "c1" {
		t.Fatalf("cover ref = %q %q %v", coverEntry, coverRevision, err)
	}
	var people int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM people WHERE lower(name)=lower($1)`, author).Scan(&people); err != nil || people != 1 {
		t.Fatalf("shared author stored %d times: %v", people, err)
	}

	// A rescan applies changed metadata and revisions without duplicating anything.
	dune.Title, dune.Series = "Dune (Revised)", ""
	result = publishStorageEbooks(t, pool, s, folder, location,
		storageEbookEntry("dune", "epub", dune, "sha256:a2"),
		storageEbookEntry("dune", "pdf", dune, "sha256:b"),
		storageEbookEntry("notes", "epub", other, "sha256:c"))
	if result != (StoragePublishResult{Updated: 1, Unchanged: 2}) {
		t.Fatalf("rescan result = %+v", result)
	}
	if err := pool.QueryRow(ctx, `SELECT title FROM media_items WHERE content_id=$1`, duneID).Scan(&title); err != nil || title != "Dune (Revised)" {
		t.Fatalf("title = %q %v", title, err)
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM item_people WHERE content_id=$1 AND kind=$2),
		(SELECT count(*) FROM ebook_series WHERE content_id=$1),
		(SELECT count(*) FROM media_files WHERE media_folder_id=$3)`, duneID, models.PersonKindAuthor, folder.ID).Scan(&credits, &seriesRows, &files); err != nil {
		t.Fatal(err)
	}
	if credits != 2 || seriesRows != 0 || files != 3 {
		t.Fatalf("after rescan credits=%d series=%d files=%d", credits, seriesRows, files)
	}
	var revision string
	if err := pool.QueryRow(ctx, `SELECT revision FROM bloem_storage_file_refs WHERE location_id=$1 AND entry_id='epub/dune'`, location.ID).Scan(&revision); err != nil || revision != "sha256:a2" {
		t.Fatalf("revision = %q %v", revision, err)
	}
}

func storageEbookContentID(t *testing.T, pool *pgxpool.Pool, folderID int, bookID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(), `SELECT f.content_id FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
		WHERE f.media_folder_id=$1 AND r.entry_id=$2`, folderID, "epub/"+bookID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
