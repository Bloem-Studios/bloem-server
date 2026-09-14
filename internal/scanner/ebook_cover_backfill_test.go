package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// backfillCoverCacher satisfies scannerImageCacher, which the Scanner requires
// as one interface even though the ebook sweep only ever calls the ebook half.
type backfillCoverCacher struct {
	fakeEbookCoverCacher
}

func (c *backfillCoverCacher) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	return "", "", fmt.Errorf("audiobook cover caching is not part of the ebook sweep")
}

// writeCoverBackfillEPUB writes a minimal EPUB. withCover controls whether the manifest
// declares a cover image, which is the difference between a book the sweep can
// satisfy and one it must record as having none.
func writeCoverBackfillEPUB(t *testing.T, path string, withCover bool) {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)

	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)

	manifest := `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"/>`
	if withCover {
		manifest += `<item id="cover" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/>`
	}
	write("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="pub-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Backfill Test Book</dc:title>
    <dc:creator>Test Author</dc:creator>
  </metadata>
  <manifest>`+manifest+`</manifest>
  <spine><itemref idref="nav"/></spine>
</package>`)
	write("OEBPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body/></html>`)
	if withCover {
		write("OEBPS/cover.jpg", "embedded-cover-bytes")
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close epub: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write epub %s: %v", path, err)
	}
}

// backfillTestBook seeds one ebook item plus one media file and returns the
// content id. Cleanup is registered by the caller's folder cleanup cascade.
func backfillTestBook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, folderID int, contentID, filePath string, modifiedAt *time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_items (content_id, type, title, status, genres)
		VALUES ($1, 'ebook', 'Backfill Test Book', 'matched', '{}'::text[])
	`, contentID); err != nil {
		t.Fatalf("seed ebook item %s: %v", contentID, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO media_files (content_id, media_folder_id, file_path, canonical_root_path, observed_root_path, file_modified_at, base_type)
		VALUES ($1, $2, $3, $3, $3, $4, 'ebook')
	`, contentID, folderID, filePath, modifiedAt); err != nil {
		t.Fatalf("seed ebook file %s: %v", contentID, err)
	}
}

// The sweep exists because the scanner's unchanged-skip means a book already in
// the catalog is never re-read. These are the behaviors that make it safe to
// run forever: it applies a cover when there is one, it records a book with no
// cover so the archive is not re-opened every pass, it re-examines that book
// once its file changes, and it never touches a book that already has artwork.
func TestBackfillMissingEbookCovers(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	t.Cleanup(pool.Close)

	var folderID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO media_folders (type, name, enabled) VALUES ('ebook', 'Cover Backfill Test', true) RETURNING id
	`).Scan(&folderID); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	suffix := time.Now().UnixNano()
	withCoverID := fmt.Sprintf("ecb-with-cover-%d", suffix)
	noCoverID := fmt.Sprintf("ecb-no-cover-%d", suffix)
	hasPosterID := fmt.Sprintf("ecb-has-poster-%d", suffix)

	t.Cleanup(func() {
		for _, id := range []string{withCoverID, noCoverID, hasPosterID} {
			_, _ = pool.Exec(ctx, `DELETE FROM media_files WHERE content_id = $1`, id)
			_, _ = pool.Exec(ctx, `DELETE FROM media_items WHERE content_id = $1`, id)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM media_folders WHERE id = $1`, folderID)
	})

	dir := t.TempDir()
	withCoverPath := filepath.Join(dir, "with-cover.epub")
	noCoverPath := filepath.Join(dir, "no-cover.epub")
	hasPosterPath := filepath.Join(dir, "has-poster.epub")
	writeCoverBackfillEPUB(t, withCoverPath, true)
	writeCoverBackfillEPUB(t, noCoverPath, false)
	writeCoverBackfillEPUB(t, hasPosterPath, true)

	modifiedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backfillTestBook(t, ctx, pool, folderID, withCoverID, withCoverPath, &modifiedAt)
	backfillTestBook(t, ctx, pool, folderID, noCoverID, noCoverPath, &modifiedAt)
	backfillTestBook(t, ctx, pool, folderID, hasPosterID, hasPosterPath, &modifiedAt)

	// Provider artwork already applied. The sweep must not select this book at
	// all: re-reading it would cost an archive open and could only ever try to
	// replace artwork that outranks anything found locally.
	if _, err := pool.Exec(ctx, `
		UPDATE media_items SET poster_path = 'artwork/provider/poster.webp' WHERE content_id = $1
	`, hasPosterID); err != nil {
		t.Fatalf("seed provider poster: %v", err)
	}

	cacher := &backfillCoverCacher{}
	scanner := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
	scanner.SetImageCacher(cacher)

	// The sweep runs against a shared test database that may hold other
	// coverless ebooks, so assertions are on this fixture's rows rather than on
	// the aggregate counts.
	if _, err := scanner.BackfillMissingEbookCovers(ctx, 500, 0); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	if got := posterPathOf(t, ctx, pool, withCoverID); got == "" {
		t.Error("book with an embedded cover still has no poster after the sweep")
	}
	if got := posterPathOf(t, ctx, pool, hasPosterID); got != "artwork/provider/poster.webp" {
		t.Errorf("provider poster = %q, want it untouched by the sweep", got)
	}
	if got := posterPathOf(t, ctx, pool, noCoverID); got != "" {
		t.Errorf("book with no cover got poster %q, want none", got)
	}

	// The whole point of the attempts table: a book with nothing to find must
	// be remembered, or the sweep re-reads it on every run forever.
	outcome, recordedMtime := coverAttemptOf(t, ctx, pool, noCoverID)
	if outcome != ebookCoverOutcomeAbsent {
		t.Errorf("recorded outcome = %q, want %q", outcome, ebookCoverOutcomeAbsent)
	}
	if recordedMtime == nil || !recordedMtime.Equal(modifiedAt) {
		t.Errorf("recorded mtime = %v, want %v", recordedMtime, modifiedAt)
	}
	if outcome, _ := coverAttemptOf(t, ctx, pool, withCoverID); outcome != "" {
		t.Errorf("successful book recorded an attempt (%q); success is already visible as a poster", outcome)
	}

	// A second sweep over an unchanged library must not re-read the book it
	// already gave up on.
	callsAfterFirst := cacher.calls
	if _, err := scanner.BackfillMissingEbookCovers(ctx, 500, 0); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if cacher.calls != callsAfterFirst {
		t.Errorf("second sweep cached %d more covers, want 0 -- the attempt record is not suppressing the re-read",
			cacher.calls-callsAfterFirst)
	}

	// Replacing the file earns a fresh attempt. The retry is gated on the
	// mtime, not on a timer, so a book that gains a cover is picked up on the
	// next pass instead of staying blank forever.
	writeCoverBackfillEPUB(t, noCoverPath, true)
	newModifiedAt := modifiedAt.Add(time.Hour)
	if _, err := pool.Exec(ctx, `
		UPDATE media_files SET file_modified_at = $2 WHERE content_id = $1
	`, noCoverID, newModifiedAt); err != nil {
		t.Fatalf("bump file mtime: %v", err)
	}
	if _, err := scanner.BackfillMissingEbookCovers(ctx, 500, 0); err != nil {
		t.Fatalf("third sweep: %v", err)
	}
	if got := posterPathOf(t, ctx, pool, noCoverID); got == "" {
		t.Error("book re-examined after its file changed still has no poster")
	}
}

func posterPathOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, contentID string) string {
	t.Helper()
	var posterPath string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(poster_path, '') FROM media_items WHERE content_id = $1
	`, contentID).Scan(&posterPath); err != nil {
		t.Fatalf("read poster for %s: %v", contentID, err)
	}
	return posterPath
}

func coverAttemptOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, contentID string) (string, *time.Time) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT outcome, file_modified_at FROM ebook_cover_backfill_attempts WHERE content_id = $1
	`, contentID)
	if err != nil {
		t.Fatalf("read attempt for %s: %v", contentID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", nil
	}
	var outcome string
	var modifiedAt *time.Time
	if err := rows.Scan(&outcome, &modifiedAt); err != nil {
		t.Fatalf("scan attempt for %s: %v", contentID, err)
	}
	return outcome, modifiedAt
}

// A deployment with no image cacher must do nothing rather than read every
// archive, find covers, fail to store them, and select the same books again.
func TestBackfillMissingEbookCoversIsInertWithoutAnImageCacher(t *testing.T) {
	scanner := NewScanner(NewFileRepository(nil), "", nil, 1, false, 0)
	stats, err := scanner.BackfillMissingEbookCovers(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("BackfillMissingEbookCovers: %v", err)
	}
	if stats.Examined != 0 {
		t.Errorf("Examined = %d, want 0 with no cacher configured", stats.Examined)
	}
}
