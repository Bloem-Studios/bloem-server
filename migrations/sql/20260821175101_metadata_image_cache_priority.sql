-- +goose NO TRANSACTION
-- +goose Up
-- Claim order for metadata_image_cache_jobs was plain FIFO (next_attempt_at,
-- id), so item/season posters, backdrops, and logos — what viewers actually
-- see in the library grid and home screen — sat behind whatever else was
-- queued first. TV libraries enqueue far more episode-still jobs than any
-- library enqueues poster-family jobs, so on a mixed backlog series posters
-- in particular could go almost untouched while episode stills (rarely seen
-- outside an episode list) consumed most of the worker pool.
--
-- job_priority (0 = high, 1 = low) lets ClaimDue prefer poster-family work
-- without abandoning FIFO within each priority band. Uses ADD COLUMN
-- ... DEFAULT (not GENERATED ALWAYS AS), which Postgres applies as a
-- metadata-only catalog default and does NOT rewrite the table — unlike the
-- GENERATED STORED columns added in 104_media_files_language_arrays.sql,
-- which explicitly required a maintenance window for that reason. Existing
-- rows are then backfilled with a single targeted UPDATE scoped to the
-- priority subset (~300k of ~1.3M rows at the time of writing), not the
-- whole table, to keep lock/IO footprint low on a table under active write
-- load from the background drain task.
-- +goose StatementBegin
ALTER TABLE public.metadata_image_cache_jobs
ADD COLUMN IF NOT EXISTS job_priority smallint NOT NULL DEFAULT 1;

UPDATE public.metadata_image_cache_jobs
SET job_priority = 0
WHERE target_type IN ('item', 'season')
  AND image_type IN ('poster', 'backdrop', 'logo')
  AND job_priority <> 0;

CREATE OR REPLACE FUNCTION public.metadata_image_cache_job_priority()
RETURNS trigger
LANGUAGE plpgsql
AS $func$
BEGIN
    IF NEW.target_type IN ('item', 'season') AND NEW.image_type IN ('poster', 'backdrop', 'logo') THEN
        NEW.job_priority := 0;
    ELSE
        NEW.job_priority := 1;
    END IF;
    RETURN NEW;
END;
$func$;

DROP TRIGGER IF EXISTS metadata_image_cache_job_priority_trg ON public.metadata_image_cache_jobs;
CREATE TRIGGER metadata_image_cache_job_priority_trg
BEFORE INSERT OR UPDATE OF target_type, image_type ON public.metadata_image_cache_jobs
FOR EACH ROW
EXECUTE FUNCTION public.metadata_image_cache_job_priority();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_index i ON i.indexrelid = c.oid
        WHERE n.nspname = 'public'
          AND c.relname = 'metadata_image_cache_jobs_priority_due_idx'
          AND NOT i.indisvalid
    ) THEN
        DROP INDEX public.metadata_image_cache_jobs_priority_due_idx;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS metadata_image_cache_jobs_priority_due_idx
ON public.metadata_image_cache_jobs (job_priority, next_attempt_at, id)
WHERE status = 'queued';

-- +goose Down
-- DROP INDEX CONCURRENTLY, like CREATE INDEX CONCURRENTLY, cannot run inside
-- a transaction block, so it must stand alone rather than share a
-- StatementBegin/End block with the other Down statements below.
DROP INDEX CONCURRENTLY IF EXISTS public.metadata_image_cache_jobs_priority_due_idx;

-- +goose StatementBegin
DROP TRIGGER IF EXISTS metadata_image_cache_job_priority_trg ON public.metadata_image_cache_jobs;
DROP FUNCTION IF EXISTS public.metadata_image_cache_job_priority();
ALTER TABLE public.metadata_image_cache_jobs DROP COLUMN IF EXISTS job_priority;
-- +goose StatementEnd
