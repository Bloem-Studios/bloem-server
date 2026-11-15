package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Silo-Server/silo-server/internal/models"
)

func shouldEnqueueEbookEnrichment(groupKeyRepair bool) bool {
	return !groupKeyRepair
}

// classifyEbookSkip extends upstream's unchangedEbookFile with Bloem's
// local-repair outcomes. A file whose grouping key predates the current
// scheme, or whose local cover upload failed on a previous scan, is
// reprocessed — but as a local repair (localRepair=true), so remote
// enrichment is not requeued for the whole library while the artwork store
// stays unavailable or a migration requests a one-time key rewrite.
func classifyEbookSkip(files []ebookSkipFile, path string, size int64, modifiedAt time.Time) (contentID string, unchanged, localRepair bool) {
	if len(files) != 1 {
		return "", false, false
	}
	file := files[0]
	if file.path != path || file.size != size || file.modifiedAt == nil || !sameFileModifiedAt(file.modifiedAt, modifiedAt) {
		return "", false, false
	}
	if file.contentID == "" {
		return "", false, false
	}
	if file.groupKeyVersion != ebookGroupKeyVersion || file.coverRetry {
		return file.contentID, false, true
	}
	if strings.EqualFold(strings.TrimSpace(file.status), "unmatched") {
		return "", false, false
	}
	return file.contentID, true, false
}

// upsertEbookMediaFileAfterCoverAttempt writes the file row and its cover
// retry state in one transaction. A failed cover leaves the file listed in
// ebook_cover_retries so the next scan reprocesses it even though the file is
// unchanged; a successful cover clears it. Retry state deliberately lives
// outside media_files.group_key_version: that column is grouping identity, and
// a file keyed under any other version is invisible to sibling-format lookups.
func (s *Scanner) upsertEbookMediaFileAfterCoverAttempt(ctx context.Context, folder *models.MediaFolder, contentID string, filePath string, size int64, modifiedAt time.Time, book *parsedEbook, groupKey string, coverErr error) error {
	mf := buildEbookMediaFile(folder, contentID, filePath, size, modifiedAt, book, groupKey)
	tx, err := s.fileRepo.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin media file upsert %s: %w", filePath, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	saved, err := s.fileRepo.UpsertTx(ctx, tx, mf)
	if err != nil {
		return fmt.Errorf("upsert media file %s: %w", filePath, err)
	}
	if coverErr != nil {
		if _, err := tx.Exec(ctx, `
			INSERT INTO ebook_cover_retries (media_file_id, failed_at)
			VALUES ($1, now())
			ON CONFLICT (media_file_id) DO UPDATE SET failed_at = EXCLUDED.failed_at
		`, saved.ID); err != nil {
			return fmt.Errorf("record ebook cover retry %s: %w", filePath, err)
		}
	} else if _, err := tx.Exec(ctx, `DELETE FROM ebook_cover_retries WHERE media_file_id = $1`, saved.ID); err != nil {
		return fmt.Errorf("clear ebook cover retry %s: %w", filePath, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit media file upsert %s: %w", filePath, err)
	}
	return nil
}

// classifyEbookFileSkip is Silo's ebookFileShouldSkip with Bloem's
// local-repair outcome (see classifyEbookSkip). The ebook scanner uses it for
// per-file reconciles; the manga scanner keeps Silo's ebookFileShouldSkip,
// since cover-retry rows are only ever recorded for ebook-library files.
func (s *Scanner) classifyEbookFileSkip(ctx context.Context, folder *models.MediaFolder, filePath string, size int64, modifiedAt time.Time) (string, bool, bool, error) {
	if s.fileRepo == nil || s.itemRepo == nil {
		return "", false, false, nil
	}
	state, err := s.fileRepo.loadEbookSkipState(ctx, folder.ID, []string{filePath})
	if err == nil {
		state, err = s.fileRepo.bloemWithEbookCoverRetries(ctx, folder.ID, []string{filePath}, state)
	}
	if err != nil {
		return "", false, false, err
	}
	contentID, unchanged, localRepair := classifyEbookSkip(state[filePath], filePath, size, modifiedAt)
	return contentID, unchanged, localRepair, nil
}

// bloemWithEbookCoverRetries sets coverRetry on the preloaded skip state for
// each file listed in ebook_cover_retries, and returns the state, or nil and
// the error so the caller falls back as on a failed preload. The ebook scan
// runs it after Silo's preload query rather than inside it, so Silo's query
// stays as Silo wrote it, one query per batch; the table is normally empty.
func (r *FileRepository) bloemWithEbookCoverRetries(ctx context.Context, folderID int, paths []string, state ebookSkipState) (ebookSkipState, error) {
	rows, err := r.pool.Query(ctx, `
  SELECT mf.observed_root_path, mf.file_path
  FROM ebook_cover_retries cr
  JOIN media_files mf ON mf.id = cr.media_file_id
  WHERE mf.media_folder_id = $1 AND mf.observed_root_path = ANY($2)
    AND mf.missing_since IS NULL
 `, folderID, paths)
	if err != nil {
		return nil, fmt.Errorf("preload ebook cover retries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var root, path string
		if err := rows.Scan(&root, &path); err != nil {
			return nil, fmt.Errorf("read ebook cover retries: %w", err)
		}
		files := state[root]
		for i := range files {
			if files[i].path == path {
				files[i].coverRetry = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ebook cover retries: %w", err)
	}
	return state, nil
}
