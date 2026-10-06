-- +goose Up
-- Literal parents include the final slash, preserving leading/dot/repeated slashes. Hash keys
-- support the full 65536-byte path and 4096-byte name limits without extensions.
-- Queries also compare the raw parent/name/suffix to reject hash collisions.
CREATE INDEX bloem_storage_entries_sidecar_name_idx
 ON bloem_storage_entries (
  source_key,last_seen_run,configuration_revision,
  md5(CASE WHEN strpos(logical_path,'/')=0 THEN '' ELSE regexp_replace(logical_path,'[^/]*$','') END),
  md5(lower(name))
 ) WHERE kind=1 AND absence_confirmed_at IS NULL;
CREATE INDEX bloem_storage_entries_sidecar_suffix_idx
 ON bloem_storage_entries (
  source_key,last_seen_run,configuration_revision,
  md5(CASE WHEN strpos(logical_path,'/')=0 THEN '' ELSE regexp_replace(logical_path,'[^/]*$','') END),
  md5(lower(substring(name FROM '\.[^.]*$')))
 ) WHERE kind=1 AND absence_confirmed_at IS NULL;

-- +goose Down
DROP INDEX bloem_storage_entries_sidecar_suffix_idx;
DROP INDEX bloem_storage_entries_sidecar_name_idx;
