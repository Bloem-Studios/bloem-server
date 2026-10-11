-- +goose Up
-- Keep every location and catalog identity. Local paths remain Silo-owned.
DROP INDEX public.library_storage_locations_folder_unique;
DROP INDEX public.library_storage_locations_source_unique;
ALTER TABLE public.library_storage_locations
 ADD COLUMN selection_revision bigint NOT NULL DEFAULT 1 CHECK (selection_revision > 0),
 ADD CONSTRAINT library_storage_locations_id_source_unique UNIQUE(id,source_key);

CREATE TABLE public.bloem_library_location_state (
 folder_id bigint PRIMARY KEY REFERENCES public.media_folders(id) ON DELETE CASCADE,
 revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0)
);
INSERT INTO public.bloem_library_location_state(folder_id) SELECT id FROM public.media_folders;

CREATE TABLE public.bloem_storage_selected_roots (
 id uuid PRIMARY KEY,
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 directory_id text NOT NULL CHECK (octet_length(directory_id) BETWEEN 1 AND 1024),
 name text NOT NULL CHECK (octet_length(name) <= 4096),
 logical_path text NOT NULL CHECK (octet_length(logical_path) <= 65536),
 enabled boolean NOT NULL DEFAULT true,
 UNIQUE(location_id,directory_id),
 UNIQUE(location_id,id)
);
INSERT INTO public.bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path)
 SELECT gen_random_uuid(),l.id,s.root_entry_id,'',''
 FROM public.library_storage_locations l JOIN public.bloem_storage_sources s ON s.key=l.source_key;

ALTER TABLE public.bloem_storage_scan_runs
 ADD COLUMN location_id uuid,
 ADD COLUMN selection_revision bigint CHECK (selection_revision > 0);
UPDATE public.bloem_storage_scan_runs r
 SET location_id=l.id,selection_revision=l.selection_revision
 FROM public.library_storage_locations l WHERE l.source_key=r.source_key;
-- Unattached historical generations cannot resume after this upgrade.
UPDATE public.bloem_storage_scan_runs
 SET state='failed',lease_epoch=lease_epoch+1,lease_until=clock_timestamp()
 WHERE state='running' AND location_id IS NULL;
ALTER TABLE public.bloem_storage_scan_runs
 ADD CONSTRAINT bloem_storage_runs_location_source_fkey FOREIGN KEY(location_id,source_key)
  REFERENCES public.library_storage_locations(id,source_key) ON DELETE SET NULL (location_id),
 ADD CONSTRAINT bloem_storage_runs_running_selection_check
  CHECK (state <> 'running' OR (location_id IS NOT NULL AND selection_revision IS NOT NULL)),
 ADD CONSTRAINT bloem_storage_runs_id_location_unique UNIQUE(id,location_id);
CREATE INDEX bloem_storage_runs_location_idx ON public.bloem_storage_scan_runs(location_id,source_key);

-- The existing library worker cascades through this owned table. Fence before
-- SET NULL retains terminal journal history. Do not lock the source here:
-- publishers lock source then folder/location before their run, while the
-- deletion worker retains the folder before cascading to the location.
-- +goose StatementBegin
CREATE FUNCTION public.bloem_storage_fence_deleted_location() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE public.bloem_storage_scan_runs
 SET state='failed',lease_epoch=lease_epoch+1,lease_until=clock_timestamp()
 WHERE location_id=OLD.id AND state='running';
 RETURN OLD;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bloem_storage_location_delete_fence BEFORE DELETE ON public.library_storage_locations
 FOR EACH ROW EXECUTE FUNCTION public.bloem_storage_fence_deleted_location();

CREATE TABLE public.bloem_storage_scan_directory_roots (
 run_id uuid NOT NULL,
 directory_id text NOT NULL,
 location_id uuid NOT NULL,
 root_id uuid NOT NULL,
 PRIMARY KEY(run_id,directory_id,root_id),
 FOREIGN KEY(run_id,directory_id) REFERENCES public.bloem_storage_scan_directories(run_id,directory_id) ON DELETE CASCADE,
 FOREIGN KEY(location_id,root_id) REFERENCES public.bloem_storage_selected_roots(location_id,id) ON DELETE CASCADE,
 FOREIGN KEY(run_id,location_id) REFERENCES public.bloem_storage_scan_runs(id,location_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX bloem_storage_directory_roots_location_root_idx ON public.bloem_storage_scan_directory_roots(location_id,root_id);
CREATE INDEX bloem_storage_directory_roots_run_location_idx ON public.bloem_storage_scan_directory_roots(run_id,location_id);
INSERT INTO public.bloem_storage_scan_directory_roots(run_id,directory_id,location_id,root_id)
 SELECT d.run_id,d.directory_id,r.location_id,s.id
 FROM public.bloem_storage_scan_directories d JOIN public.bloem_storage_scan_runs r ON r.id=d.run_id
 JOIN public.bloem_storage_selected_roots s ON s.location_id=r.location_id;

CREATE TABLE public.bloem_storage_location_directories (
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 directory_id text NOT NULL CHECK (octet_length(directory_id) BETWEEN 1 AND 1024),
 configuration_revision bigint NOT NULL CHECK (configuration_revision > 0),
 selection_revision bigint NOT NULL CHECK (selection_revision > 0),
 last_complete_run uuid REFERENCES public.bloem_storage_scan_runs(id) ON DELETE SET NULL,
 PRIMARY KEY(location_id,directory_id)
);
CREATE INDEX bloem_storage_location_directories_run_idx ON public.bloem_storage_location_directories(last_complete_run);
INSERT INTO public.bloem_storage_location_directories(location_id,directory_id,configuration_revision,selection_revision,last_complete_run)
 SELECT DISTINCT ON (r.location_id,d.directory_id) r.location_id,d.directory_id,r.configuration_revision,r.selection_revision,r.id
 FROM public.bloem_storage_scan_directories d JOIN public.bloem_storage_scan_runs r ON r.id=d.run_id
 WHERE r.location_id IS NOT NULL AND r.state='complete' AND d.complete
 ORDER BY r.location_id,d.directory_id,r.created_at DESC,r.id;

CREATE TABLE public.bloem_storage_location_entries (
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 4096),
 logical_path text NOT NULL CHECK (octet_length(logical_path) <= 65536),
 kind integer NOT NULL CHECK (kind IN (1,2)),
 size bigint NOT NULL CHECK (size >= 0),
 modified_unix_nano bigint NOT NULL,
 revision text NOT NULL CHECK (octet_length(revision) <= 4096),
 configuration_revision bigint NOT NULL CHECK (configuration_revision > 0),
 selection_revision bigint NOT NULL CHECK (selection_revision > 0),
 last_seen_run uuid REFERENCES public.bloem_storage_scan_runs(id) ON DELETE SET NULL,
 absence_confirmed_at timestamptz,
 PRIMARY KEY(location_id,entry_id)
);
CREATE INDEX bloem_storage_location_entries_run_idx ON public.bloem_storage_location_entries(last_seen_run);
INSERT INTO public.bloem_storage_location_entries
 SELECT l.id,e.entry_id,e.name,e.logical_path,e.kind,e.size,e.modified_unix_nano,e.revision,
 e.configuration_revision,l.selection_revision,e.last_seen_run,e.absence_confirmed_at
 FROM public.bloem_storage_entries e JOIN public.library_storage_locations l ON l.source_key=e.source_key;

-- Retained traversal edges describe opaque identities, never path ancestry.
CREATE TABLE public.bloem_storage_location_edges (
 location_id uuid NOT NULL,
 parent_directory_id text NOT NULL,
 entry_id text NOT NULL,
 PRIMARY KEY(location_id,parent_directory_id,entry_id),
 FOREIGN KEY(location_id,parent_directory_id) REFERENCES public.bloem_storage_location_directories(location_id,directory_id) ON DELETE CASCADE,
 FOREIGN KEY(location_id,entry_id) REFERENCES public.bloem_storage_location_entries(location_id,entry_id) ON DELETE CASCADE
);
CREATE INDEX bloem_storage_location_edges_entry_idx ON public.bloem_storage_location_edges(location_id,entry_id);

ALTER TABLE public.bloem_storage_file_refs
 ADD CONSTRAINT bloem_storage_file_refs_location_file_unique UNIQUE(location_id,media_file_id);
CREATE TABLE public.bloem_storage_file_roots (
 location_id uuid NOT NULL,
 media_file_id bigint NOT NULL,
 root_id uuid NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 PRIMARY KEY(location_id,media_file_id,root_id),
 FOREIGN KEY(location_id,media_file_id) REFERENCES public.bloem_storage_file_refs(location_id,media_file_id) ON DELETE CASCADE,
 FOREIGN KEY(location_id,root_id) REFERENCES public.bloem_storage_selected_roots(location_id,id) ON DELETE CASCADE
);
CREATE INDEX bloem_storage_file_roots_location_root_idx ON public.bloem_storage_file_roots(location_id,root_id);
INSERT INTO public.bloem_storage_file_roots(location_id,media_file_id,root_id)
 SELECT f.location_id,f.media_file_id,r.id FROM public.bloem_storage_file_refs f
 JOIN public.bloem_storage_selected_roots r ON r.location_id=f.location_id;

ALTER TABLE public.bloem_storage_change_tokens
 ADD COLUMN location_id uuid,
 ADD COLUMN selection_revision bigint CHECK (selection_revision > 0);
UPDATE public.bloem_storage_change_tokens t
 SET location_id=l.id,selection_revision=l.selection_revision
 FROM public.library_storage_locations l WHERE l.source_key=t.source_key;
-- Tokens without a surviving library are unusable; journal and catalog stay.
DELETE FROM public.bloem_storage_change_tokens WHERE location_id IS NULL;
ALTER TABLE public.bloem_storage_change_tokens
 DROP CONSTRAINT bloem_storage_change_tokens_pkey,
 ALTER COLUMN location_id SET NOT NULL,
 ALTER COLUMN selection_revision SET NOT NULL,
 ADD PRIMARY KEY(location_id,directory_id),
 ADD CONSTRAINT bloem_storage_tokens_location_source_fkey FOREIGN KEY(location_id,source_key)
  REFERENCES public.library_storage_locations(id,source_key) ON DELETE CASCADE;
CREATE INDEX bloem_storage_change_tokens_source_idx ON public.bloem_storage_change_tokens(source_key);
CREATE INDEX bloem_storage_change_tokens_location_source_idx ON public.bloem_storage_change_tokens(location_id,source_key);

-- +goose Down
LOCK TABLE public.bloem_storage_sources,public.library_storage_locations,public.media_folder_paths,public.bloem_storage_selected_roots,
 public.bloem_storage_scan_runs,public.bloem_storage_scan_directories,public.bloem_storage_scan_directory_roots,
 public.bloem_storage_location_directories,public.bloem_storage_location_entries,public.bloem_storage_location_edges,
 public.bloem_storage_file_refs,public.bloem_storage_file_roots,public.bloem_storage_change_tokens,
 public.bloem_library_location_state IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.library_storage_locations GROUP BY folder_id HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM public.library_storage_locations GROUP BY source_key HAVING count(*)>1)
 OR EXISTS(SELECT 1 FROM public.library_storage_locations l
  JOIN public.media_folder_paths p ON p.media_folder_id=l.folder_id WHERE p.path<>'')
 OR EXISTS(SELECT 1 FROM public.library_storage_locations l
  JOIN public.bloem_storage_sources s ON s.key=l.source_key
  WHERE (SELECT count(*) FROM public.bloem_storage_selected_roots r WHERE r.location_id=l.id)<>1
   OR NOT EXISTS(SELECT 1 FROM public.bloem_storage_selected_roots r
    WHERE r.location_id=l.id AND r.directory_id=s.root_entry_id AND r.enabled))
 OR EXISTS(SELECT 1 FROM public.bloem_storage_file_refs f
  WHERE NOT EXISTS(SELECT 1 FROM public.bloem_storage_file_roots m
   JOIN public.bloem_storage_selected_roots r ON r.location_id=m.location_id AND r.id=m.root_id
   WHERE m.location_id=f.location_id AND m.media_file_id=f.media_file_id AND m.enabled AND r.enabled))
 OR EXISTS(SELECT 1 FROM public.bloem_storage_location_edges)
 OR EXISTS(SELECT 1 FROM public.bloem_storage_location_entries e
  JOIN public.library_storage_locations l ON l.id=e.location_id
  LEFT JOIN public.bloem_storage_entries s ON s.source_key=l.source_key AND s.entry_id=e.entry_id
  WHERE s.entry_id IS NULL OR (e.name,e.logical_path,e.kind,e.size,e.modified_unix_nano,e.revision,e.configuration_revision,e.last_seen_run,e.absence_confirmed_at)
   IS DISTINCT FROM (s.name,s.logical_path,s.kind,s.size,s.modified_unix_nano,s.revision,s.configuration_revision,s.last_seen_run,s.absence_confirmed_at)) THEN
  RAISE EXCEPTION 'mixed library locations retained: refusing lossy rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX public.bloem_storage_change_tokens_source_idx;
DROP INDEX public.bloem_storage_change_tokens_location_source_idx;
ALTER TABLE public.bloem_storage_change_tokens
 DROP CONSTRAINT bloem_storage_tokens_location_source_fkey,
 DROP CONSTRAINT bloem_storage_change_tokens_pkey,
 DROP COLUMN location_id,
 DROP COLUMN selection_revision,
 ADD PRIMARY KEY(source_key,directory_id);
DROP TABLE public.bloem_storage_file_roots;
ALTER TABLE public.bloem_storage_file_refs DROP CONSTRAINT bloem_storage_file_refs_location_file_unique;
DROP TABLE public.bloem_storage_location_edges;
DROP TABLE public.bloem_storage_location_entries;
DROP TABLE public.bloem_storage_location_directories;
DROP TABLE public.bloem_storage_scan_directory_roots;
DROP TRIGGER bloem_storage_location_delete_fence ON public.library_storage_locations;
DROP FUNCTION public.bloem_storage_fence_deleted_location();
DROP INDEX public.bloem_storage_runs_location_idx;
ALTER TABLE public.bloem_storage_scan_runs
 DROP CONSTRAINT bloem_storage_runs_id_location_unique,
 DROP CONSTRAINT bloem_storage_runs_running_selection_check,
 DROP CONSTRAINT bloem_storage_runs_location_source_fkey,
 DROP COLUMN location_id,
 DROP COLUMN selection_revision;
DROP TABLE public.bloem_storage_selected_roots;
DROP TABLE public.bloem_library_location_state;
ALTER TABLE public.library_storage_locations
 DROP CONSTRAINT library_storage_locations_id_source_unique,
 DROP COLUMN selection_revision;
CREATE UNIQUE INDEX library_storage_locations_folder_unique ON public.library_storage_locations(folder_id);
CREATE UNIQUE INDEX library_storage_locations_source_unique ON public.library_storage_locations(source_key);
