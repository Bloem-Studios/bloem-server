-- +goose NO TRANSACTION

-- +goose Up
-- Native storage scans write every entry and scan directory again, so an index
-- no query reads only costs writes. On production these four took about 850 MB
-- and none had been scanned in a month.
--
-- The sidecar lookups and the ingestion walk lost their queries when library
-- storage locations replaced native onboarding (f59aaafce, 2026-10-07): entries
-- are read by their primary key and by source.
DROP INDEX CONCURRENTLY IF EXISTS public.bloem_storage_entries_sidecar_name_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.bloem_storage_entries_sidecar_suffix_idx;
DROP INDEX CONCURRENTLY IF EXISTS public.bloem_storage_ingestion_entries_idx;
-- An exact copy of bloem_storage_pending_directories, which stays.
DROP INDEX CONCURRENTLY IF EXISTS public.bloem_storage_scan_directories_pending_idx;

-- +goose Down
-- The migrations that created these drop them without IF EXISTS on their way
-- down, so they come back as they were.
CREATE INDEX CONCURRENTLY IF NOT EXISTS bloem_storage_scan_directories_pending_idx
 ON public.bloem_storage_scan_directories (run_id, directory_id) WHERE NOT complete;
CREATE INDEX CONCURRENTLY IF NOT EXISTS bloem_storage_ingestion_entries_idx
 ON public.bloem_storage_entries (source_key, last_seen_run, entry_id) WHERE kind = 1;
CREATE INDEX CONCURRENTLY IF NOT EXISTS bloem_storage_entries_sidecar_suffix_idx
 ON public.bloem_storage_entries (
  source_key, last_seen_run, configuration_revision,
  md5(CASE WHEN strpos(logical_path, '/') = 0 THEN '' ELSE regexp_replace(logical_path, '[^/]*$', '') END),
  md5(lower(substring(name FROM '\.[^.]*$')))
 ) WHERE kind = 1 AND absence_confirmed_at IS NULL;
CREATE INDEX CONCURRENTLY IF NOT EXISTS bloem_storage_entries_sidecar_name_idx
 ON public.bloem_storage_entries (
  source_key, last_seen_run, configuration_revision,
  md5(CASE WHEN strpos(logical_path, '/') = 0 THEN '' ELSE regexp_replace(logical_path, '[^/]*$', '') END),
  md5(lower(name))
 ) WHERE kind = 1 AND absence_confirmed_at IS NULL;
