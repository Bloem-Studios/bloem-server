-- +goose NO TRANSACTION
-- +goose Up
-- Corrects the column choice in 20260914114340_index_artwork_source_paths.sql.
--
-- The artwork revision GC's reference check (artworkReferenceUnionSQL) filters
-- on each sweep surface's pathCol -- poster_path, backdrop_path, logo_path --
-- not its sourceCol. The earlier migration indexed the *_source_path columns,
-- which that query never touches, so it changed nothing: cleanup runs stayed at
-- ~280s.
--
-- Only media_items is large enough to matter (625k rows on the reference
-- deployment; every other surface is under 500 rows), and the union scans it
-- three times. Without an index a batched check over 10,000 candidate paths ran
-- for more than ten minutes; with these three it returns in ~310ms.
--
-- Partial on IS NOT NULL: rows with no artwork can never match a candidate
-- path, and excluding them keeps the indexes to 89/15/11 MB.
--
-- CONCURRENTLY so this does not take a write lock on a running server; that
-- requires NO TRANSACTION. Note CREATE INDEX CONCURRENTLY waits for every
-- in-flight transaction to commit, so a long-running GC pass will block it
-- until that pass finishes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_items_poster_path_gc ON public.media_items (poster_path) WHERE poster_path IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_items_backdrop_path_gc ON public.media_items (backdrop_path) WHERE backdrop_path IS NOT NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_items_logo_path_gc ON public.media_items (logo_path) WHERE logo_path IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_media_items_poster_path_gc;
DROP INDEX CONCURRENTLY IF EXISTS idx_media_items_backdrop_path_gc;
DROP INDEX CONCURRENTLY IF EXISTS idx_media_items_logo_path_gc;
