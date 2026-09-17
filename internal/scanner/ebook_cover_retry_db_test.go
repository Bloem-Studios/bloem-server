package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5/pgxpool"
)

type toggleEbookCoverCacher struct {
	fakeEbookCoverCacher
}

func (f *toggleEbookCoverCacher) CacheAudiobookCover(context.Context, []byte, string) (string, string, error) {
	return "", "", errors.New("unexpected audiobook cover")
}

type countingEbookEnrichmentQueue struct {
	enqueued int
}

func (q *countingEbookEnrichmentQueue) Enqueue(context.Context, string, int) error {
	q.enqueued++
	return nil
}

func (q *countingEbookEnrichmentQueue) ReconcileMissing(context.Context, int, int, int) (int, int, bool, error) {
	return 0, 0, false, nil
}

// TestEbookCoverRetryKeepsSiblingFormatGrouping catches a failed cover upload
// changing the file's grouping identity. The retry marker used to be a
// decremented group_key_version, which hid the file from the sibling-format
// lookup, so a second format of the same book became a separate item.
func TestEbookCoverRetryKeepsSiblingFormatGrouping(t *testing.T) {
	pool := newDeadRootTestPool(t)
	ctx := context.Background()
	folderID := seedDeadRootTestFolder(t, pool, "ebooks", "Ebook cover retry grouping")
	root := t.TempDir()
	folder := &models.MediaFolder{ID: folderID, Paths: []string{root}, Type: "ebooks", Name: "Ebook cover retry grouping", Enabled: true}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM media_items mi
			USING media_files mf
			WHERE mf.content_id = mi.content_id AND mf.media_folder_id = $1`, folderID)
	})

	epubPath := filepath.Join(root, "The Test Ebook.epub")
	if err := os.Rename(writeTestEPUBWithCover(t, "cover.png", "image/png", []byte("cover bytes")), epubPath); err != nil {
		t.Fatalf("move epub into library: %v", err)
	}

	cacher := &toggleEbookCoverCacher{}
	cacher.err = errors.New("artwork store unavailable")
	queue := &countingEbookEnrichmentQueue{}
	scanner := NewScanner(NewFileRepository(pool), "", nil, 1, false, 0)
	scanner.SetImageCacher(cacher)
	scanner.SetEbookEnrichmentQueue(queue)

	if err := scanner.ScanEbookFolder(ctx, folder); err != nil {
		t.Fatalf("scan with failing cover store: %v", err)
	}
	if got := ebookCoverRetryCount(t, pool, folderID); got != 1 {
		t.Fatalf("cover retries after failed upload = %d, want 1", got)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT group_key_version FROM media_files WHERE file_path = $1`, epubPath).Scan(&version); err != nil {
		t.Fatalf("read epub group key version: %v", err)
	}
	if version != ebookGroupKeyVersion {
		t.Fatalf("epub group_key_version after failed cover = %d, want %d", version, ebookGroupKeyVersion)
	}

	// A second format arrives while the cover is still pending retry.
	pdfPath := filepath.Join(root, "The Test Ebook.pdf")
	if err := os.WriteFile(pdfPath, []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if err := scanner.ScanFile(ctx, pdfPath, folder); err != nil {
		t.Fatalf("scan sibling pdf: %v", err)
	}
	if got := ebookContentIDCount(t, pool, folderID); got != 1 {
		t.Fatalf("distinct items for epub+pdf after cover failure = %d, want 1 grouped item", got)
	}

	// The unchanged epub is retried, without re-queueing remote enrichment.
	enqueuedBeforeRetry := queue.enqueued
	callsBeforeRetry := cacher.calls
	cacher.err = nil
	if err := scanner.ScanEbookFolder(ctx, folder); err != nil {
		t.Fatalf("scan with recovered cover store: %v", err)
	}
	if cacher.calls == callsBeforeRetry {
		t.Fatal("unchanged epub with a pending cover retry was not reprocessed")
	}
	if queue.enqueued != enqueuedBeforeRetry {
		t.Fatalf("cover retry enqueued enrichment %d times, want 0", queue.enqueued-enqueuedBeforeRetry)
	}
	if got := ebookCoverRetryCount(t, pool, folderID); got != 0 {
		t.Fatalf("cover retries after successful upload = %d, want 0", got)
	}
	if got := ebookContentIDCount(t, pool, folderID); got != 1 {
		t.Fatalf("distinct items after cover retry = %d, want 1", got)
	}

	// Once the cover is stored the retry is finished: nothing is reprocessed.
	callsAfterRetry := cacher.calls
	if err := scanner.ScanEbookFolder(ctx, folder); err != nil {
		t.Fatalf("scan after cover retry: %v", err)
	}
	if cacher.calls != callsAfterRetry || queue.enqueued != enqueuedBeforeRetry {
		t.Fatalf("scan after completed retry reprocessed files: cover calls %d->%d, enqueued %d->%d",
			callsAfterRetry, cacher.calls, enqueuedBeforeRetry, queue.enqueued)
	}
}

func ebookCoverRetryCount(t *testing.T, pool *pgxpool.Pool, folderID int) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM ebook_cover_retries r
		JOIN media_files mf ON mf.id = r.media_file_id
		WHERE mf.media_folder_id = $1`, folderID).Scan(&count); err != nil {
		t.Fatalf("count ebook cover retries: %v", err)
	}
	return count
}

func ebookContentIDCount(t *testing.T, pool *pgxpool.Pool, folderID int) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(DISTINCT content_id)
		FROM media_files
		WHERE media_folder_id = $1 AND missing_since IS NULL`, folderID).Scan(&count); err != nil {
		t.Fatalf("count ebook items: %v", err)
	}
	return count
}
