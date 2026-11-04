-- +goose NO TRANSACTION

-- +goose Up
-- 20261104065534_bloem_content_id_collation and
-- 20261104080323_bloem_content_id_references_collation changed the type of
-- these content ID columns, which drops each column's statistics. Until they are
-- sampled again the planner estimates the joins those migrations fix from
-- defaults, and autovacuum may not analyze a table that rarely changes for a
-- long time. ANALYZE samples rather than scans; each runs on its own, after the
-- collation migrations released their locks. Silo's version of the change
-- (Silo #3480) analyzes the tables it changes itself.
ANALYZE public.metadata_enrichment_state;
ANALYZE public.audiobook_enrichment_state;
ANALYZE public.media_item_tmdb_collections;
ANALYZE public.manga_enrichment_state;
ANALYZE public.manga_chapters;
ANALYZE public.audiobook_item_file_stats;
ANALYZE public.item_videos;
ANALYZE public.local_sidecar_fingerprints;
ANALYZE public.media_extras;
ANALYZE public.media_item_aliases;
ANALYZE public.media_item_backdrops;
ANALYZE public.media_item_rating_sources;

-- +goose Down
-- Statistics need no undoing.
SELECT 1;
