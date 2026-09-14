-- +goose NO TRANSACTION
-- +goose Up
-- The ebook scanner extracts a cover from the book itself -- a sidecar image
-- beside the file, or the cover embedded in the EPUB/archive -- but only while
-- reconciling a file whose size or mtime changed. Every book ingested before
-- that path was reachable is therefore behind the scanner's unchanged-skip and
-- will never be revisited, because the skip is keyed on the file, not on
-- whether the item has artwork. On the deployment this was written for that is
-- 166,414 of 167,603 ebooks.
--
-- backfill_ebook_covers sweeps those items. A book whose cover is applied
-- leaves the candidate set on its own, since the sweep selects on an empty
-- poster_path. A book that has no cover to find does not, and without a record
-- of the attempt the sweep would re-open the same ~150k archives on every run,
-- forever. This table is that record: only misses are stored, and a stored miss
-- is retried when the file behind it changes.
--
-- file_modified_at is the mtime of the file the attempt actually read. The
-- sweep retries when the candidate file's mtime IS DISTINCT FROM this value, so
-- replacing the book, or adding a new format alongside it, earns a fresh
-- attempt while an unchanged library converges to zero work.
CREATE TABLE IF NOT EXISTS ebook_cover_backfill_attempts (
    content_id       TEXT PRIMARY KEY REFERENCES media_items(content_id) ON DELETE CASCADE,
    attempted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- NULL when the scanner recorded no mtime for the file. Such a row is never
    -- retried on mtime alone (NULL IS DISTINCT FROM NULL is false), which is
    -- the desired terminal state for a file we cannot tell apart from itself.
    file_modified_at TIMESTAMPTZ,
    -- 'no_cover': the file was read and held no cover and had no sidecar.
    -- 'unreadable': the file could not be opened or parsed.
    outcome          TEXT NOT NULL
);

-- The sweep's candidate query and its remaining-work count both begin by
-- finding ebooks with no poster. Once the backlog is drained that answer is
-- zero, and without this index proving it means walking every ebook row on
-- every run, forever -- 1.5s per execution on a 167k-book catalog, for a
-- question whose answer is nothing.
--
-- The predicate is spelled exactly as the queries spell it so the planner can
-- match it. Partial on the empty-poster condition keeps the index proportional
-- to the work outstanding rather than to the library: it shrinks as covers are
-- applied and is near-empty in the steady state this task converges to.
--
-- CONCURRENTLY so this does not take a write lock on a running server, which
-- requires NO TRANSACTION.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_items_ebook_missing_poster
    ON media_items (content_id)
    WHERE type = 'ebook' AND (poster_path IS NULL OR poster_path = '');

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_media_items_ebook_missing_poster;
DROP TABLE IF EXISTS ebook_cover_backfill_attempts;
