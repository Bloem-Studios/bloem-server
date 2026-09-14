package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// The ebook scanner pulls a cover out of the book itself -- a sidecar image
// beside the file, or the cover embedded in the EPUB/archive -- but it does so
// inside reconcileEbookFile, after the size/mtime skip. A book whose file has
// not changed since it was ingested is never opened again, so every ebook that
// entered the catalog before that extraction path was reachable keeps an empty
// poster no matter how many times the library is rescanned: the skip is keyed
// on the file, not on whether the item has artwork.
//
// This sweep is the way out, and the way it stays out. It selects ebooks with
// no poster, opens one file per book, and runs the same applyEbookLocalCover
// the scanner runs -- so sidecar-beats-embedded precedence, the local/ebooks
// ownership rule that keeps provider artwork winning, and the thumbhash dedupe
// all behave identically here. Nothing about extraction is restated.
//
// It also stays cheap forever. A book whose cover is applied drops out of the
// candidate set on its own, because the set is defined by an empty poster. A
// book with no cover to find would not, so a miss is recorded in
// ebook_cover_backfill_attempts and retried only when the file behind it
// changes. An untouched library converges to zero candidates and stays there.

// EbookCoverBackfillStats is one sweep's outcome.
type EbookCoverBackfillStats struct {
	// Examined is the number of books whose file this sweep opened.
	Examined int
	// Applied is the number that yielded cover bytes.
	Applied int
	// Absent is the number read successfully that held no cover and had no
	// sidecar. Each is recorded so it is not read again until its file changes.
	Absent int
	// Unreadable is the number whose file could not be opened or parsed. These
	// are recorded too: a file that cannot be parsed today parses no better on
	// the next pass, and a replacement changes the mtime that gates the retry.
	Unreadable int
	// Remaining, when Known, is how many candidates were left after this sweep.
	Remaining      int
	RemainingKnown bool
}

// ebookCoverBackfillCandidate is one book to examine, with the file the sweep
// will read and the mtime that gates any retry of it.
type ebookCoverBackfillCandidate struct {
	ContentID      string
	FilePath       string
	FileModifiedAt *time.Time
}

// selectEbookCoverBackfillSQL picks one file per coverless ebook.
//
// The LATERAL prefers formats whose cover this pipeline can actually reach
// without a sidecar: parseEbookFile reads an EPUB's manifest and an archive's
// first image, while .mobi, .azw3 and .pdf resolve only when a sidecar image
// sits beside them. Ordering by id after that keeps the choice stable across
// runs, so the mtime recorded against a miss belongs to the file a later run
// will compare against.
//
// Files marked missing are excluded: reading them fails by definition, and
// recording an attempt against a file that is merely offline would suppress the
// real attempt owed when the mount returns.
//
// The retry rule is IS DISTINCT FROM rather than a timestamp comparison, so a
// file whose mtime moves backwards (a restore, a rsync --times from an older
// copy) also earns a fresh attempt, and a NULL mtime on both sides -- a file
// the scanner recorded no mtime for -- compares equal and is never retried on
// mtime alone. That is the intended terminal state for a file that cannot be
// told apart from itself.
const selectEbookCoverBackfillSQL = `
	SELECT f.content_id, f.file_path, f.file_modified_at
	FROM media_items i
	JOIN LATERAL (
		SELECT mf.content_id, mf.file_path, mf.file_modified_at
		FROM media_files mf
		WHERE mf.content_id = i.content_id
		  AND mf.missing_since IS NULL
		ORDER BY
			CASE
				WHEN lower(mf.file_path) LIKE '%.epub' THEN 0
				WHEN lower(mf.file_path) LIKE '%.cbz' THEN 1
				ELSE 2
			END,
			mf.id
		LIMIT 1
	) f ON TRUE
	LEFT JOIN ebook_cover_backfill_attempts a ON a.content_id = i.content_id
	WHERE i.type = 'ebook'
	  AND (i.poster_path IS NULL OR i.poster_path = '')
	  AND (a.content_id IS NULL OR f.file_modified_at IS DISTINCT FROM a.file_modified_at)
	ORDER BY i.content_id
	LIMIT $1
`

// countEbookCoverBackfillSQL is selectEbookCoverBackfillSQL without the page
// limit, for reporting how much work is left after a bounded sweep.
//
// It costs one LATERAL probe per remaining candidate, so it is O(backlog) and
// runs once per execution rather than per page. The empty-poster predicate is
// spelled to match idx_media_items_ebook_missing_poster, which is what keeps a
// drained library -- where the answer is zero and the scan would otherwise have
// to walk every ebook to prove it -- from paying for the reassurance.
const countEbookCoverBackfillSQL = `
	SELECT COUNT(*)
	FROM media_items i
	JOIN LATERAL (
		SELECT mf.content_id, mf.file_modified_at
		FROM media_files mf
		WHERE mf.content_id = i.content_id
		  AND mf.missing_since IS NULL
		ORDER BY
			CASE
				WHEN lower(mf.file_path) LIKE '%.epub' THEN 0
				WHEN lower(mf.file_path) LIKE '%.cbz' THEN 1
				ELSE 2
			END,
			mf.id
		LIMIT 1
	) f ON TRUE
	LEFT JOIN ebook_cover_backfill_attempts a ON a.content_id = i.content_id
	WHERE i.type = 'ebook'
	  AND (i.poster_path IS NULL OR i.poster_path = '')
	  AND (a.content_id IS NULL OR f.file_modified_at IS DISTINCT FROM a.file_modified_at)
`

const (
	ebookCoverOutcomeAbsent     = "no_cover"
	ebookCoverOutcomeUnreadable = "unreadable"
)

// BackfillMissingEbookCovers extracts covers for ebooks that have none,
// examining at most limit books and stopping early if budget elapses or ctx is
// cancelled. It is safe to call on an interval: work it completes does not come
// back, and work it cannot complete is recorded so it is not retried until the
// underlying file changes.
//
// A book that yields no cover is not an error. The returned error is reserved
// for failures that make the sweep itself untrustworthy -- the candidate query,
// or a context that ended -- so the task can distinguish "nothing to do" from
// "could not look".
func (s *Scanner) BackfillMissingEbookCovers(ctx context.Context, limit int, budget time.Duration) (EbookCoverBackfillStats, error) {
	var stats EbookCoverBackfillStats
	if s == nil || s.fileRepo == nil || s.fileRepo.Pool() == nil {
		return stats, nil
	}
	// Extraction needs somewhere to put the bytes. Without a cacher every
	// candidate would read its file, find a cover, fail to store it, and be
	// selected again on the next pass -- so do nothing at all instead.
	if s.imageCacher == nil || s.itemRepo == nil {
		return stats, nil
	}
	if limit <= 0 {
		return stats, nil
	}

	candidates, err := s.selectEbookCoverBackfillCandidates(ctx, limit)
	if err != nil {
		return stats, err
	}

	deadline := time.Time{}
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			break
		}
		s.backfillOneEbookCover(ctx, candidate, &stats)
	}

	if remaining, err := s.countEbookCoverBackfillCandidates(ctx); err == nil {
		stats.Remaining = remaining
		stats.RemainingKnown = true
	}
	return stats, nil
}

func (s *Scanner) selectEbookCoverBackfillCandidates(ctx context.Context, limit int) ([]ebookCoverBackfillCandidate, error) {
	rows, err := s.fileRepo.Pool().Query(ctx, selectEbookCoverBackfillSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("selecting ebook cover backfill candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]ebookCoverBackfillCandidate, 0, limit)
	for rows.Next() {
		var candidate ebookCoverBackfillCandidate
		if err := rows.Scan(&candidate.ContentID, &candidate.FilePath, &candidate.FileModifiedAt); err != nil {
			return nil, fmt.Errorf("scanning ebook cover backfill candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading ebook cover backfill candidates: %w", err)
	}
	return candidates, nil
}

func (s *Scanner) countEbookCoverBackfillCandidates(ctx context.Context) (int, error) {
	var remaining int
	if err := s.fileRepo.Pool().QueryRow(ctx, countEbookCoverBackfillSQL).Scan(&remaining); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("counting ebook cover backfill candidates: %w", err)
	}
	return remaining, nil
}

// backfillOneEbookCover reads one book and applies or records its outcome. It
// never returns an error: a single unreadable book must not end the sweep, and
// every terminal outcome it can reach is already recorded as an attempt.
func (s *Scanner) backfillOneEbookCover(ctx context.Context, candidate ebookCoverBackfillCandidate, stats *EbookCoverBackfillStats) {
	stats.Examined++

	// parseEbookFile is the scanner's own reader, so a file it cannot parse
	// here is one the scanner could not have parsed either. Record the attempt
	// rather than retrying a corrupt or truncated file every six hours.
	parsed, parseErr := parseEbookFile(candidate.FilePath)
	if parseErr != nil {
		stats.Unreadable++
		s.recordEbookCoverAttempt(ctx, candidate, ebookCoverOutcomeUnreadable)
		slog.DebugContext(ctx, "ebook cover backfill: unreadable book",
			"component", "scanner",
			"content_id", candidate.ContentID,
			"path", candidate.FilePath,
			"error", parseErr,
		)
		return
	}

	found, err := applyEbookLocalCover(ctx, s.itemRepo, s.imageCacher, candidate.ContentID, candidate.FilePath, &parsed)
	if err != nil {
		// The cover existed but could not be stored -- an S3 outage, a
		// transient database error. Deliberately no attempt row: the book still
		// has a cover to find, and suppressing it would turn a passing failure
		// into a permanent one.
		stats.Unreadable++
		slog.WarnContext(ctx, "ebook cover backfill: cover upload failed",
			"component", "scanner",
			"content_id", candidate.ContentID,
			"path", candidate.FilePath,
			"error", err,
		)
		return
	}
	if !found {
		stats.Absent++
		s.recordEbookCoverAttempt(ctx, candidate, ebookCoverOutcomeAbsent)
		return
	}
	stats.Applied++
}

// recordEbookCoverAttempt marks a book as examined so it is not read again
// until its file changes. A failure to record is logged and swallowed: the
// sweep's real work is already done, and the only cost of a lost record is one
// wasted read on a later pass.
func (s *Scanner) recordEbookCoverAttempt(ctx context.Context, candidate ebookCoverBackfillCandidate, outcome string) {
	if _, err := s.fileRepo.Pool().Exec(ctx, `
		INSERT INTO ebook_cover_backfill_attempts (content_id, attempted_at, file_modified_at, outcome)
		VALUES ($1, NOW(), $2, $3)
		ON CONFLICT (content_id) DO UPDATE
		SET attempted_at = NOW(),
		    file_modified_at = EXCLUDED.file_modified_at,
		    outcome = EXCLUDED.outcome
	`, candidate.ContentID, candidate.FileModifiedAt, outcome); err != nil {
		slog.WarnContext(ctx, "ebook cover backfill: could not record attempt",
			"component", "scanner",
			"content_id", candidate.ContentID,
			"outcome", outcome,
			"error", err,
		)
	}
}
