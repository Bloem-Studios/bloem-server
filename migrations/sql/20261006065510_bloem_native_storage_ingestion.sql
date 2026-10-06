-- +goose Up
ALTER TABLE bloem_storage_sources ADD COLUMN discovery_run_id uuid REFERENCES bloem_storage_scan_runs(id) ON DELETE RESTRICT;
CREATE TABLE bloem_storage_ingestion (
 run_id uuid NOT NULL REFERENCES bloem_storage_scan_runs(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL REFERENCES bloem_storage_bindings(id) ON DELETE CASCADE,
 lease_epoch bigint NOT NULL CHECK (lease_epoch>0),
 owner text NOT NULL CHECK (octet_length(owner) BETWEEN 1 AND 1024),
 lease_until timestamptz NOT NULL,
 last_entry_id text NOT NULL DEFAULT '' CHECK (octet_length(last_entry_id)<=1024),
 pending_entry_id text NOT NULL DEFAULT '' CHECK (octet_length(pending_entry_id)<=1024),
 pending_revision text NOT NULL DEFAULT '' CHECK (octet_length(pending_revision)<=4096),
 pending_token uuid,
 complete boolean NOT NULL DEFAULT false,
 PRIMARY KEY(run_id,binding_id),
 CHECK ((pending_token IS NULL AND pending_entry_id='' AND pending_revision='')
     OR (pending_token IS NOT NULL AND pending_entry_id<>'' AND pending_revision<>'')),
 CHECK (NOT complete OR pending_token IS NULL)
);
CREATE INDEX bloem_storage_ingestion_entries_idx
 ON bloem_storage_entries(source_key,last_seen_run,entry_id) WHERE kind=1;

-- +goose Down
LOCK TABLE bloem_storage_sources, bloem_storage_scan_runs, bloem_storage_bindings, bloem_storage_ingestion IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM bloem_storage_ingestion) THEN
  RAISE EXCEPTION 'native ingestion checkpoints retained: refusing destructive rollback';
 END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE bloem_storage_sources DROP COLUMN discovery_run_id;
DROP TABLE bloem_storage_ingestion;
DROP INDEX bloem_storage_ingestion_entries_idx;
