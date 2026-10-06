-- +goose Up
CREATE TABLE bloem_storage_sources (
 key uuid PRIMARY KEY,
 installation_id bigint REFERENCES plugin_installations(id) ON DELETE SET NULL,
 plugin_id text NOT NULL CHECK (octet_length(plugin_id) BETWEEN 1 AND 1024),
 provider_source_id text NOT NULL CHECK (octet_length(provider_source_id) BETWEEN 1 AND 1024),
 root_entry_id text NOT NULL CHECK (octet_length(root_entry_id) BETWEEN 1 AND 1024),
 configuration_revision bigint NOT NULL CHECK (configuration_revision>0),
 enabled boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_storage_sources_installation_idx ON bloem_storage_sources(installation_id);

CREATE TABLE bloem_storage_bindings (
 id uuid PRIMARY KEY,
 source_key uuid NOT NULL REFERENCES bloem_storage_sources(key) ON DELETE RESTRICT,
 folder_id bigint NOT NULL REFERENCES media_folders(id) ON DELETE CASCADE,
 UNIQUE(source_key,folder_id)
);
CREATE INDEX bloem_storage_bindings_folder_idx ON bloem_storage_bindings(folder_id);

CREATE TABLE bloem_storage_scan_runs (
 id uuid PRIMARY KEY,
 source_key uuid NOT NULL REFERENCES bloem_storage_sources(key) ON DELETE RESTRICT,
 configuration_revision bigint NOT NULL CHECK (configuration_revision>0),
 state text NOT NULL CHECK (state IN ('running','complete','failed')),
 lease_epoch bigint NOT NULL CHECK (lease_epoch>0),
 owner text NOT NULL CHECK (octet_length(owner) BETWEEN 1 AND 1024),
 lease_until timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);
CREATE UNIQUE INDEX bloem_storage_one_running_generation ON bloem_storage_scan_runs(source_key) WHERE state='running';
CREATE INDEX bloem_storage_runs_source_idx ON bloem_storage_scan_runs(source_key);

CREATE TABLE bloem_storage_entries (
 source_key uuid NOT NULL REFERENCES bloem_storage_sources(key) ON DELETE RESTRICT,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 4096),
 logical_path text NOT NULL CHECK (octet_length(logical_path)<=65536),
 kind integer NOT NULL CHECK (kind IN (1,2)),
 size bigint NOT NULL CHECK (size>=0),
 modified_unix_nano bigint NOT NULL,
 revision text NOT NULL CHECK (octet_length(revision)<=4096),
 configuration_revision bigint NOT NULL CHECK (configuration_revision>0),
 last_seen_run uuid NOT NULL REFERENCES bloem_storage_scan_runs(id) ON DELETE RESTRICT,
 absence_confirmed_at timestamptz,
 PRIMARY KEY(source_key,entry_id)
);
CREATE INDEX bloem_storage_entries_run_idx ON bloem_storage_entries(last_seen_run);

CREATE TABLE bloem_storage_file_refs (
 media_file_id bigint PRIMARY KEY REFERENCES media_files(id) ON DELETE CASCADE,
 binding_id uuid NOT NULL REFERENCES bloem_storage_bindings(id) ON DELETE RESTRICT,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 revision text NOT NULL CHECK (octet_length(revision) BETWEEN 1 AND 4096),
 logical_path text NOT NULL CHECK (octet_length(logical_path)<=65536),
 configuration_revision bigint NOT NULL CHECK (configuration_revision>0),
 UNIQUE(binding_id,entry_id)
);

CREATE TABLE bloem_storage_scan_directories (
 run_id uuid NOT NULL REFERENCES bloem_storage_scan_runs(id) ON DELETE CASCADE,
 directory_id text NOT NULL CHECK (octet_length(directory_id) BETWEEN 1 AND 1024),
 current_cursor text NOT NULL DEFAULT '' CHECK (octet_length(current_cursor)<=4096),
 complete boolean NOT NULL DEFAULT false,
 processed_count bigint NOT NULL DEFAULT 0 CHECK (processed_count>=0),
 PRIMARY KEY(run_id,directory_id)
);
CREATE INDEX bloem_storage_pending_directories ON bloem_storage_scan_directories(run_id,directory_id) WHERE NOT complete;

CREATE TABLE bloem_storage_scan_cursors (
 run_id uuid NOT NULL,
 directory_id text NOT NULL,
 cursor_sha256 bytea NOT NULL CHECK (octet_length(cursor_sha256)=32),
 cursor text NOT NULL CHECK (octet_length(cursor)<=4096),
 page_sha256 bytea NOT NULL CHECK (octet_length(page_sha256)=32),
 PRIMARY KEY(run_id,directory_id,cursor_sha256),
 FOREIGN KEY(run_id,directory_id) REFERENCES bloem_storage_scan_directories(run_id,directory_id) ON DELETE CASCADE
);

-- +goose Down
-- Serialize the empty-data check with writes before any table can be dropped.
LOCK TABLE bloem_storage_sources, bloem_storage_bindings,
 bloem_storage_scan_runs, bloem_storage_entries, bloem_storage_file_refs,
 bloem_storage_scan_directories, bloem_storage_scan_cursors IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM bloem_storage_sources)
 OR EXISTS(SELECT 1 FROM bloem_storage_bindings)
 OR EXISTS(SELECT 1 FROM bloem_storage_entries)
 OR EXISTS(SELECT 1 FROM bloem_storage_file_refs)
 OR EXISTS(SELECT 1 FROM bloem_storage_scan_runs)
 OR EXISTS(SELECT 1 FROM bloem_storage_scan_directories)
 OR EXISTS(SELECT 1 FROM bloem_storage_scan_cursors) THEN
  RAISE EXCEPTION 'native storage data retained: refusing destructive rollback';
 END IF;
END;
$$;
-- +goose StatementEnd
DROP TABLE bloem_storage_scan_cursors;
DROP TABLE bloem_storage_scan_directories;
DROP TABLE bloem_storage_file_refs;
DROP TABLE bloem_storage_entries;
DROP TABLE bloem_storage_scan_runs;
DROP TABLE bloem_storage_bindings;
DROP TABLE bloem_storage_sources;
