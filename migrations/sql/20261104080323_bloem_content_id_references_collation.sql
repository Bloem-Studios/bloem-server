-- Silo's 20261104070617_content_id_references_c_collation (Silo #3480) ahead of
-- its merge, with the same Up: every text column with a foreign key to
-- media_items, seasons or episodes whose collation differs from the column it
-- references moves to COLLATE "C". 20261104065534_bloem_content_id_collation
-- already moved the enrichment state and box set tables; this finishes the rest
-- (aliases, sidecar fingerprints, audiobook file stats, item videos, extras,
-- backdrops, rating sources). When Silo's migration arrives it finds nothing to
-- change.
--
-- Only the collation changes: no table is rewritten and the foreign keys are not
-- rechecked, but the indexes on these columns are rebuilt, about 4.5 seconds per
-- GB. Each change recreates the foreign key's triggers on the referenced table,
-- so the referenced tables are locked first, against reads and writes, until the
-- migration commits.

-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    c RECORD;
    parents regclass[];
BEGIN
    CREATE TEMP TABLE _bloem_content_id_collation_targets ON COMMIT DROP AS
    SELECT DISTINCT con.conrelid::regclass AS table_name, a.attname AS column_name, con.confrelid::regclass AS parent
    FROM pg_constraint con
    CROSS JOIN LATERAL unnest(con.conkey, con.confkey) AS k(attnum, refnum)
    JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
    JOIN pg_attribute r ON r.attrelid = con.confrelid AND r.attnum = k.refnum
    WHERE con.contype = 'f'
      AND con.confrelid IN ('public.media_items'::regclass, 'public.seasons'::regclass, 'public.episodes'::regclass)
      AND a.atttypid = 'text'::regtype
      AND a.attcollation <> r.attcollation;

    SELECT array_agg(DISTINCT parent ORDER BY parent) INTO parents FROM _bloem_content_id_collation_targets;
    IF parents IS NULL THEN
        RETURN;
    END IF;
    FOR c IN SELECT unnest(parents) AS parent LOOP
        EXECUTE format('LOCK TABLE %s IN ACCESS EXCLUSIVE MODE', c.parent);
    END LOOP;

    FOR c IN SELECT DISTINCT table_name, column_name FROM _bloem_content_id_collation_targets ORDER BY 1, 2 LOOP
        EXECUTE format('ALTER TABLE %s ALTER COLUMN %I TYPE text COLLATE "C"', c.table_name, c.column_name);
    END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- Returns the seven columns the Up moves on Bloem's schema to the default
-- collation they were created with.
-- +goose StatementBegin
ALTER TABLE public.audiobook_item_file_stats ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.item_videos ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.local_sidecar_fingerprints ALTER COLUMN item_id TYPE text COLLATE "default";
ALTER TABLE public.media_extras ALTER COLUMN parent_id TYPE text COLLATE "default";
ALTER TABLE public.media_item_aliases ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.media_item_backdrops ALTER COLUMN content_id TYPE text COLLATE "default";
ALTER TABLE public.media_item_rating_sources ALTER COLUMN content_id TYPE text COLLATE "default";
-- +goose StatementEnd
