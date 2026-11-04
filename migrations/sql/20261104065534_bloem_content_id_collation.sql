-- Silo stores content IDs as text COLLATE "C" so join keys match on type and
-- collation (docs/architecture/deterministic-content-id.md). These Silo tables
-- were created later with the database's default collation, so a join from
-- media_items.content_id cannot use their content ID index. Bloem's hourly bulk
-- enrichment probe then compared every matched item with every row of
-- metadata_enrichment_state, which took 15 minutes a run on Bloem's catalog;
-- the audiobook enrichment and box set reads have the same NOT EXISTS shape.
--
-- Only the collation changes: the values and their equality stay the same, so
-- the tables are not rewritten, only the indexes on these columns are rebuilt.
-- All of them are small. Columns already in "C" (a fresh install creates the
-- manga ones that way) are left as they are.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    target record;
BEGIN
    -- Each change recreates the foreign key's triggers on media_items, which
    -- locks it exclusively; take that lock first so the lock order is fixed.
    LOCK TABLE public.media_items IN ACCESS EXCLUSIVE MODE;
    FOR target IN
        SELECT a.attrelid::regclass AS tbl, a.attname AS col
        FROM pg_attribute a
        LEFT JOIN pg_collation c ON c.oid = a.attcollation
        WHERE (a.attrelid, a.attname) IN (
            ('public.metadata_enrichment_state'::regclass, 'content_id'),
            ('public.audiobook_enrichment_state'::regclass, 'content_id'),
            ('public.media_item_tmdb_collections'::regclass, 'content_id'),
            ('public.manga_enrichment_state'::regclass, 'content_id'),
            ('public.manga_chapters'::regclass, 'chapter_content_id'),
            ('public.manga_chapters'::regclass, 'series_content_id'))
          AND c.collname IS DISTINCT FROM 'C'
    LOOP
        EXECUTE format('ALTER TABLE %s ALTER COLUMN %I TYPE text COLLATE "C"', target.tbl, target.col);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.metadata_enrichment_state ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.audiobook_enrichment_state ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.media_item_tmdb_collections ALTER COLUMN content_id TYPE text COLLATE "default";
-- +goose StatementEnd
