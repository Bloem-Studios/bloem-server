-- +goose NO TRANSACTION
-- +goose Up
-- The artwork revision GC asks "does any catalog record still reference this
-- original path?" before deleting an object. That check is a UNION over ten
-- surfaces filtering on *_source_path, and it runs twice per candidate: once
-- batched for the claimed batch, then again per candidate under its row lock.
--
-- None of those columns was indexed, so every check sequentially scanned
-- episodes (2.2M), people (1.2M), media_items (625k) and seasons (139k). One
-- cleanup run took ~280s to collect 100 objects, which is why a backlog of
-- 6.7M candidates covering 9.26M objects had never drained since the GC landed
-- on 2026-07-16 -- the queue grows faster than it empties, and the only visible
-- symptom is the bucket growing.
--
-- CONCURRENTLY because episodes and people are large and this must not hold a
-- write lock on a running server; NO TRANSACTION is required for it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_items_poster_source_path_idx ON public.media_items (poster_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_items_backdrop_source_path_idx ON public.media_items (backdrop_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_items_logo_source_path_idx ON public.media_items (logo_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_item_localizations_poster_source_path_idx ON public.media_item_localizations (poster_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_item_localizations_backdrop_source_path_idx ON public.media_item_localizations (backdrop_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_item_localizations_logo_source_path_idx ON public.media_item_localizations (logo_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS seasons_poster_source_path_idx ON public.seasons (poster_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS season_localizations_poster_source_path_idx ON public.season_localizations (poster_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS episodes_still_source_path_idx ON public.episodes (still_source_path);
CREATE INDEX CONCURRENTLY IF NOT EXISTS people_photo_source_path_idx ON public.people (photo_source_path);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.people_photo_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.episodes_still_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.season_localizations_poster_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.seasons_poster_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_item_localizations_logo_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_item_localizations_backdrop_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_item_localizations_poster_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_items_logo_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_items_backdrop_source_path_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.media_items_poster_source_path_idx;
