-- +goose NO TRANSACTION
-- +goose Up
-- 20260914202106 added this table for a standalone ebook-cover sweep. That
-- sweep is gone: the repair belongs to the scanner, which already reprocesses a
-- file whose group_key_version is behind and already parallelises across files.
-- The sweep re-solved that problem on its own, worse -- single-threaded, it
-- managed 155 books in a ten-minute budget against a 166,414-book backlog.
--
-- Dropping rather than leaving it: an empty table nothing writes is a trap for
-- the next person, who has to work out whether it is load-bearing.
DROP INDEX CONCURRENTLY IF EXISTS idx_media_items_ebook_missing_poster;
DROP TABLE IF EXISTS ebook_cover_backfill_attempts;

-- +goose Down
-- +goose StatementBegin
-- The sweep this served is deleted; recreating its table would recreate a
-- mechanism nothing drives.
SELECT 1;
-- +goose StatementEnd
