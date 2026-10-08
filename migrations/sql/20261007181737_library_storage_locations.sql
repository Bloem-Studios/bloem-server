-- +goose Up
-- Storage-source libraries become ordinary libraries whose location is a
-- storage source instead of a filesystem path. This removes the separate
-- native onboarding model: the native library marker, publication permits and
-- every bloem_native_* guard trigger, which fired on core catalog tables for
-- every library. The binding table becomes the library's storage location.
--
-- Only an unpublished native catalog can be reset. A database that already
-- holds native file references refuses the migration rather than dropping
-- catalog rows that users may have read or bookmarked.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.bloem_storage_file_refs) THEN
  RAISE EXCEPTION 'native storage catalog already published: refusing to reset native libraries';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER bloem_native_bloem_native_publication_permits_complete ON public.bloem_native_publication_permits;
DROP TRIGGER bloem_native_bloem_storage_file_refs_complete ON public.bloem_storage_file_refs;
DROP TRIGGER bloem_native_media_files_complete ON public.media_files;
DROP TRIGGER bloem_native_media_item_libraries_complete ON public.media_item_libraries;
DROP TRIGGER bloem_native_media_items_complete ON public.media_items;
DROP TRIGGER bloem_native_media_extras_book ON public.media_extras;
DROP TRIGGER bloem_native_watch_provider_dropped_items_book ON public.watch_provider_dropped_items;
DROP TRIGGER bloem_native_user_dropped_series_book ON public.user_dropped_series;
DROP TRIGGER bloem_native_series_root_match_queue_folder ON public.series_root_match_queue;
DROP TRIGGER bloem_native_media_root_overrides_folder ON public.media_root_overrides;
DROP TRIGGER bloem_native_skipped_media_roots_folder ON public.skipped_media_roots;
DROP TRIGGER bloem_native_media_group_overrides_folder ON public.media_group_overrides;
DROP TRIGGER bloem_native_observed_media_locations_folder ON public.observed_media_locations;
DROP TRIGGER bloem_native_media_group_locations_folder ON public.media_group_locations;
DROP TRIGGER bloem_native_scanned_media_groups_folder ON public.scanned_media_groups;
DROP TRIGGER bloem_native_scanned_media_roots_folder ON public.scanned_media_roots;
DROP TRIGGER bloem_native_media_item_groups_folder ON public.media_item_groups;
DROP TRIGGER bloem_native_media_item_roots_folder ON public.media_item_roots;
DROP TRIGGER bloem_native_episode_libraries_video ON public.episode_libraries;
DROP TRIGGER bloem_native_episodes_video ON public.episodes;
DROP TRIGGER bloem_native_seasons_video ON public.seasons;
DROP TRIGGER bloem_native_media_item_provider_ids_identity ON public.media_item_provider_ids;
DROP TRIGGER bloem_native_ebook_reader_progress_identity ON public.ebook_reader_progress;
DROP TRIGGER bloem_native_watch_provider_dropped_items_identity ON public.watch_provider_dropped_items;
DROP TRIGGER bloem_native_watch_provider_rating_items_identity ON public.watch_provider_rating_items;
DROP TRIGGER bloem_native_user_dropped_series_identity ON public.user_dropped_series;
DROP TRIGGER bloem_native_user_series_playback_preferences_identity ON public.user_series_playback_preferences;
DROP TRIGGER bloem_native_user_subtitle_preferences_identity ON public.user_subtitle_preferences;
DROP TRIGGER bloem_native_user_audio_preferences_identity ON public.user_audio_preferences;
DROP TRIGGER bloem_native_user_history_hidden_items_identity ON public.user_history_hidden_items;
DROP TRIGGER bloem_native_user_home_item_dismissals_identity ON public.user_home_item_dismissals;
DROP TRIGGER bloem_native_library_collection_items_identity ON public.library_collection_items;
DROP TRIGGER bloem_native_user_personal_collection_items_identity ON public.user_personal_collection_items;
DROP TRIGGER bloem_native_user_ratings_identity ON public.user_ratings;
DROP TRIGGER bloem_native_user_watchlist_identity ON public.user_watchlist;
DROP TRIGGER bloem_native_user_favorites_identity ON public.user_favorites;
DROP TRIGGER bloem_native_user_watch_progress_identity ON public.user_watch_progress;
DROP TRIGGER bloem_native_user_watch_history_identity ON public.user_watch_history;
DROP TRIGGER bloem_native_downloads_identity ON public.downloads;
DROP TRIGGER bloem_native_user_downloads_identity ON public.user_downloads;
DROP TRIGGER bloem_native_admin_playback_history_identity ON public.admin_playback_history;
DROP TRIGGER bloem_native_bloem_native_publication_permits_guard ON public.bloem_native_publication_permits;
DROP TRIGGER bloem_native_bloem_storage_file_refs_guard ON public.bloem_storage_file_refs;
DROP TRIGGER bloem_native_media_item_libraries_guard ON public.media_item_libraries;
DROP TRIGGER bloem_native_media_files_guard ON public.media_files;
DROP TRIGGER bloem_native_media_items_guard ON public.media_items;
DROP TRIGGER bloem_native_bloem_storage_bindings_guard ON public.bloem_storage_bindings;
DROP TRIGGER bloem_native_bloem_native_libraries_guard ON public.bloem_native_libraries;
DROP TRIGGER bloem_native_media_folder_paths_folder ON public.media_folder_paths;
DROP TRIGGER bloem_native_media_folders_changed ON public.media_folders;
DROP TRIGGER bloem_native_media_folders_protected ON public.media_folders;
DROP FUNCTION public.bloem_native_check_publication_complete();
DROP FUNCTION public.bloem_native_check_association_complete();
DROP FUNCTION public.bloem_native_guard_permit();
DROP FUNCTION public.bloem_native_guard_extra();
DROP FUNCTION public.bloem_native_guard_book_drop();
DROP FUNCTION public.bloem_native_guard_video();
DROP FUNCTION public.bloem_native_guard_ref();
DROP FUNCTION public.bloem_native_guard_file();
DROP FUNCTION public.bloem_native_guard_member();
DROP FUNCTION public.bloem_native_guard_item();
DROP FUNCTION public.bloem_native_guard_binding();
DROP FUNCTION public.bloem_native_guard_marker();
DROP FUNCTION public.bloem_native_guard_folder_child();
DROP FUNCTION public.bloem_native_guard_folder_changed();
DROP FUNCTION public.bloem_native_guard_folder_protected();
DROP FUNCTION public.bloem_native_guard_identity();
DROP FUNCTION public.bloem_native_lock_item_keys(text[]);
DROP FUNCTION public.bloem_native_item_class(text);
DROP FUNCTION public.bloem_native_folder_class(bigint);
DROP TABLE public.bloem_native_publication_permits;

-- Native libraries were pathless folders created by the onboarding flow. They
-- hold no published files (checked above); recreate them as libraries with a
-- storage location.
CREATE TEMPORARY TABLE native_library_reset ON COMMIT DROP AS
 SELECT folder_id AS id FROM public.bloem_native_libraries;
DROP TABLE public.bloem_native_libraries;
-- The default-organization entitlement references the folder ON DELETE
-- RESTRICT; release it explicitly, as ordinary library deletion does.
DELETE FROM public.entitlement_bundle_members m
 USING native_library_reset r WHERE m.media_folder_id = r.id;
DELETE FROM public.organization_entitlements e
 USING native_library_reset r WHERE e.media_folder_id = r.id;
DELETE FROM public.media_folders f
 USING native_library_reset r WHERE f.id = r.id;

-- Discovery restarts from scratch under the new listing protocol.
UPDATE public.bloem_storage_sources SET discovery_run_id = NULL;
-- Storage scans publish each listed page in the page's own transaction, so the
-- separate per-file ingestion queue is no longer used.
DROP TABLE public.bloem_storage_ingestion;
DELETE FROM public.bloem_storage_entries;
DELETE FROM public.bloem_storage_scan_runs;

ALTER TABLE public.bloem_storage_bindings RENAME TO library_storage_locations;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT bloem_storage_bindings_pkey TO library_storage_locations_pkey;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT bloem_storage_bindings_source_key_fkey TO library_storage_locations_source_key_fkey;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT bloem_storage_bindings_folder_id_fkey TO library_storage_locations_folder_id_fkey;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT bloem_storage_bindings_source_key_folder_id_key TO library_storage_locations_source_key_folder_id_key;
ALTER INDEX public.bloem_storage_bindings_folder_idx RENAME TO library_storage_locations_folder_idx;
-- One storage location per library, and one library per source: a scan
-- publishes the source's listing straight into its library's catalog.
ALTER INDEX public.bloem_native_binding_folder_unique RENAME TO library_storage_locations_folder_unique;
CREATE UNIQUE INDEX library_storage_locations_source_unique ON public.library_storage_locations(source_key);

ALTER TABLE public.bloem_storage_file_refs RENAME COLUMN binding_id TO location_id;
-- Deleting a library cascades to both its files and its location; a file
-- reference must not block the location half of that cascade.
ALTER TABLE public.bloem_storage_file_refs
 DROP CONSTRAINT bloem_storage_file_refs_binding_id_fkey,
 ADD CONSTRAINT bloem_storage_file_refs_location_id_fkey FOREIGN KEY (location_id)
  REFERENCES public.library_storage_locations(id) ON DELETE CASCADE;

-- +goose Down
-- Restores the native onboarding schema exactly as it stood before this
-- migration (definitions taken from a database at 20261007094051). Native
-- library markers, permits and ingestion rows are not recovered, so only a
-- database without storage locations or published storage files can go back.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.library_storage_locations) OR EXISTS(SELECT 1 FROM public.bloem_storage_file_refs) THEN
  RAISE EXCEPTION 'library storage locations: cannot restore native onboarding while storage libraries exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX public.library_storage_locations_source_unique;
ALTER TABLE public.bloem_storage_file_refs DROP CONSTRAINT bloem_storage_file_refs_location_id_fkey;
ALTER TABLE public.bloem_storage_file_refs RENAME COLUMN location_id TO binding_id;
ALTER INDEX public.library_storage_locations_folder_unique RENAME TO bloem_native_binding_folder_unique;
ALTER INDEX public.library_storage_locations_folder_idx RENAME TO bloem_storage_bindings_folder_idx;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT library_storage_locations_source_key_folder_id_key TO bloem_storage_bindings_source_key_folder_id_key;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT library_storage_locations_folder_id_fkey TO bloem_storage_bindings_folder_id_fkey;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT library_storage_locations_source_key_fkey TO bloem_storage_bindings_source_key_fkey;
ALTER TABLE public.library_storage_locations RENAME CONSTRAINT library_storage_locations_pkey TO bloem_storage_bindings_pkey;
ALTER TABLE public.library_storage_locations RENAME TO bloem_storage_bindings;

-- +goose StatementBegin
CREATE TABLE public.bloem_native_libraries (
    folder_id bigint NOT NULL,
    owner_id uuid NOT NULL,
    creation_key uuid NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    initialized boolean DEFAULT false NOT NULL,
    deleting_job_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT bloem_native_libraries_revision_check CHECK ((revision > 0))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE public.bloem_native_publication_permits (
    xid bigint NOT NULL,
    item_key text NOT NULL,
    existing_item_key text,
    item_version timestamp with time zone,
    folder_key bigint NOT NULL,
    library_revision bigint NOT NULL,
    folder_owner_id uuid NOT NULL,
    source_key uuid NOT NULL,
    source_owner_id uuid NOT NULL,
    binding_id uuid NOT NULL,
    discovery_run_id uuid NOT NULL,
    installation_key bigint NOT NULL,
    runtime_generation bigint NOT NULL,
    configuration_revision bigint CONSTRAINT bloem_native_publication_permit_configuration_revision_not_null NOT NULL,
    protocol_version integer NOT NULL,
    ingestion_owner text NOT NULL,
    ingestion_epoch bigint NOT NULL,
    lease_token uuid NOT NULL,
    entry_id text NOT NULL,
    entry_revision text NOT NULL,
    logical_path text NOT NULL,
    catalog_location text NOT NULL,
    entry_kind integer NOT NULL,
    entry_size bigint NOT NULL,
    entry_modified_unix_nano bigint CONSTRAINT bloem_native_publication_perm_entry_modified_unix_nano_not_null NOT NULL,
    normalized_modified_at timestamp with time zone CONSTRAINT bloem_native_publication_permit_normalized_modified_at_not_null NOT NULL,
    parsed_container text NOT NULL,
    content_group_key text NOT NULL,
    file_shape jsonb NOT NULL,
    sidecar_shape jsonb NOT NULL,
    retained_file_key bigint,
    phase text DEFAULT 'armed'::text NOT NULL,
    item_checked boolean DEFAULT false NOT NULL,
    member_checked boolean DEFAULT false NOT NULL,
    ref_checked boolean DEFAULT false NOT NULL,
    finished boolean DEFAULT false NOT NULL,
    attempted_file_key bigint,
    stored_file_key bigint,
    CONSTRAINT bloem_native_publication_permits_attempted_file_key_check CHECK ((attempted_file_key > 0)),
    CONSTRAINT bloem_native_publication_permits_catalog_location_check CHECK ((catalog_location <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_check CHECK (((existing_item_key IS NULL) = (item_version IS NULL))),
    CONSTRAINT bloem_native_publication_permits_configuration_revision_check CHECK ((configuration_revision > 0)),
    CONSTRAINT bloem_native_publication_permits_content_group_key_check CHECK ((content_group_key <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_entry_id_check CHECK ((entry_id <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_entry_kind_check CHECK ((entry_kind = 1)),
    CONSTRAINT bloem_native_publication_permits_entry_revision_check CHECK ((entry_revision <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_entry_size_check CHECK ((entry_size >= 0)),
    CONSTRAINT bloem_native_publication_permits_file_shape_check CHECK ((jsonb_typeof(file_shape) = 'object'::text)),
    CONSTRAINT bloem_native_publication_permits_folder_key_check CHECK ((folder_key > 0)),
    CONSTRAINT bloem_native_publication_permits_ingestion_epoch_check CHECK ((ingestion_epoch > 0)),
    CONSTRAINT bloem_native_publication_permits_ingestion_owner_check CHECK ((ingestion_owner <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_installation_key_check CHECK ((installation_key > 0)),
    CONSTRAINT bloem_native_publication_permits_item_key_check CHECK ((item_key <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_library_revision_check CHECK ((library_revision > 0)),
    CONSTRAINT bloem_native_publication_permits_logical_path_check CHECK ((logical_path <> ''::text)),
    CONSTRAINT bloem_native_publication_permits_parsed_container_check CHECK ((parsed_container = ANY (ARRAY['epub'::text, 'pdf'::text]))),
    CONSTRAINT bloem_native_publication_permits_phase_check CHECK ((phase = ANY (ARRAY['armed'::text, 'item'::text, 'member'::text, 'file'::text, 'ref'::text, 'finished'::text]))),
    CONSTRAINT bloem_native_publication_permits_protocol_version_check CHECK ((protocol_version = 1)),
    CONSTRAINT bloem_native_publication_permits_retained_file_key_check CHECK ((retained_file_key > 0)),
    CONSTRAINT bloem_native_publication_permits_runtime_generation_check CHECK ((runtime_generation > 0)),
    CONSTRAINT bloem_native_publication_permits_sidecar_shape_check CHECK ((jsonb_typeof(sidecar_shape) = 'array'::text)),
    CONSTRAINT bloem_native_publication_permits_stored_file_key_check CHECK ((stored_file_key > 0))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE public.bloem_storage_ingestion (
    run_id uuid NOT NULL,
    binding_id uuid NOT NULL,
    lease_epoch bigint NOT NULL,
    owner text NOT NULL,
    lease_until timestamp with time zone NOT NULL,
    last_entry_id text DEFAULT ''::text NOT NULL,
    pending_entry_id text DEFAULT ''::text NOT NULL,
    pending_revision text DEFAULT ''::text NOT NULL,
    pending_token uuid,
    complete boolean DEFAULT false NOT NULL,
    CONSTRAINT bloem_storage_ingestion_check CHECK ((((pending_token IS NULL) AND (pending_entry_id = ''::text) AND (pending_revision = ''::text)) OR ((pending_token IS NOT NULL) AND (pending_entry_id <> ''::text) AND (pending_revision <> ''::text)))),
    CONSTRAINT bloem_storage_ingestion_check1 CHECK (((NOT complete) OR (pending_token IS NULL))),
    CONSTRAINT bloem_storage_ingestion_last_entry_id_check CHECK ((octet_length(last_entry_id) <= 1024)),
    CONSTRAINT bloem_storage_ingestion_lease_epoch_check CHECK ((lease_epoch > 0)),
    CONSTRAINT bloem_storage_ingestion_owner_check CHECK (((octet_length(owner) >= 1) AND (octet_length(owner) <= 1024))),
    CONSTRAINT bloem_storage_ingestion_pending_entry_id_check CHECK ((octet_length(pending_entry_id) <= 1024)),
    CONSTRAINT bloem_storage_ingestion_pending_revision_check CHECK ((octet_length(pending_revision) <= 4096))
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_native_libraries
    ADD CONSTRAINT bloem_native_libraries_creation_key_key UNIQUE (creation_key);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_native_libraries
    ADD CONSTRAINT bloem_native_libraries_pkey PRIMARY KEY (folder_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_native_publication_permits
    ADD CONSTRAINT bloem_native_publication_permits_pkey PRIMARY KEY (xid);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_storage_ingestion
    ADD CONSTRAINT bloem_storage_ingestion_pkey PRIMARY KEY (run_id, binding_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_native_libraries
    ADD CONSTRAINT bloem_native_libraries_folder_id_fkey FOREIGN KEY (folder_id) REFERENCES public.media_folders(id) ON DELETE RESTRICT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_native_libraries
    ADD CONSTRAINT bloem_native_libraries_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES public.resource_owners(id) ON DELETE RESTRICT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_storage_file_refs
    ADD CONSTRAINT bloem_storage_file_refs_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.bloem_storage_bindings(id) ON DELETE RESTRICT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_storage_ingestion
    ADD CONSTRAINT bloem_storage_ingestion_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES public.bloem_storage_bindings(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE ONLY public.bloem_storage_ingestion
    ADD CONSTRAINT bloem_storage_ingestion_run_id_fkey FOREIGN KEY (run_id) REFERENCES public.bloem_storage_scan_runs(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_check_association_complete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE n jsonb:=to_jsonb(NEW); k text; fid bigint; saved media_files%ROWTYPE; ref bloem_storage_file_refs%ROWTYPE;
BEGIN
 IF TG_TABLE_NAME='media_items' THEN
  IF NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=NEW.content_id) THEN RETURN NULL; END IF;
  k:=NEW.content_id;
 ELSIF TG_TABLE_NAME='media_item_libraries' THEN
  IF NOT EXISTS(SELECT 1 FROM media_item_libraries WHERE content_id=NEW.content_id AND media_folder_id=NEW.media_folder_id) THEN RETURN NULL; END IF;
  k:=NEW.content_id; fid:=NEW.media_folder_id;
 ELSE
  SELECT * INTO saved FROM media_files WHERE id=CASE WHEN TG_TABLE_NAME='media_files' THEN (n->>'id')::bigint ELSE (n->>'media_file_id')::bigint END;
  IF saved.id IS NULL THEN RETURN NULL; END IF;
  k:=saved.content_id; fid:=saved.media_folder_id;
  SELECT * INTO ref FROM bloem_storage_file_refs WHERE media_file_id=saved.id;
  IF bloem_native_folder_class(fid)<>'local' OR ref.media_file_id IS NOT NULL THEN
   IF bloem_native_folder_class(fid)<>'native' OR saved.episode_id IS NOT NULL OR saved.extra_id IS NOT NULL
   OR saved.missing_since IS NOT NULL OR saved.container NOT IN('epub','pdf') OR saved.probe_source<>'native'
   OR saved.canonical_root_path<>saved.file_path OR saved.observed_root_path<>saved.file_path
   OR ref.media_file_id IS NULL OR NOT EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE id=ref.binding_id AND folder_id=fid)
   OR NOT EXISTS(SELECT 1 FROM bloem_storage_entries e JOIN bloem_storage_bindings b ON b.source_key=e.source_key
    WHERE b.id=ref.binding_id AND e.entry_id=ref.entry_id AND e.revision=ref.revision
    AND e.logical_path=ref.logical_path AND e.configuration_revision=ref.configuration_revision AND e.kind=1)
   THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  END IF;
  IF bloem_native_item_class(saved.episode_id)<>'local' OR bloem_native_item_class(saved.extra_id)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
 END IF;
 IF bloem_native_item_class(k)='inconsistent' OR (fid IS NOT NULL AND bloem_native_folder_class(fid)='inconsistent')
 OR (fid IS NOT NULL AND bloem_native_folder_class(fid)='native' AND bloem_native_item_class(k)<>'native')
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_check_publication_complete() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE:=NEW; m bloem_native_libraries%ROWTYPE; f media_folders%ROWTYPE;
 src bloem_storage_sources%ROWTYPE; saved media_files%ROWTYPE; shape jsonb;
BEGIN
 IF EXISTS(SELECT 1 FROM bloem_native_publication_permits WHERE xid=p.xid) THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

SELECT * INTO m FROM bloem_native_libraries WHERE folder_id=p.folder_key;
SELECT * INTO f FROM media_folders WHERE id=p.folder_key;
SELECT * INTO src FROM bloem_storage_sources WHERE key=p.source_key;
IF m.folder_id IS NULL OR NOT m.initialized OR m.revision<>p.library_revision OR m.revision<>3
 OR m.deleting_job_id IS NOT NULL OR m.owner_id<>p.folder_owner_id OR f.owner_id<>m.owner_id OR NOT f.enabled OR f.type<>'ebook'
 OR EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=p.folder_key)
 OR src.key IS NULL OR NOT src.enabled OR src.owner_id<>p.source_owner_id OR src.installation_id IS DISTINCT FROM p.installation_key
 OR src.configuration_revision<>p.configuration_revision OR src.discovery_run_id IS DISTINCT FROM p.discovery_run_id
 OR NOT EXISTS(SELECT 1 FROM plugin_installations i JOIN bloem_storage_installations ni ON ni.installation_id=i.id
  WHERE i.id=p.installation_key AND i.owner_id=p.source_owner_id AND i.enabled AND i.kind='plugin'
  AND i.plugin_id=src.plugin_id AND i.runtime_generation=p.runtime_generation
  AND ni.owner_id=i.owner_id AND ni.protocol_version=p.protocol_version AND p.protocol_version=1)
 OR NOT (
EXISTS(SELECT 1 FROM resource_owners fo JOIN resource_owners so ON so.id=src.owner_id
 WHERE fo.id=f.owner_id AND fo.revision>0 AND so.revision>0
 AND ((fo.kind='platform' AND fo.organization_id IS NULL AND so.kind='platform' AND so.organization_id IS NULL)
 OR (fo.kind='organization' AND fo.organization_id IS NOT NULL
  AND EXISTS(SELECT 1 FROM organizations WHERE id=fo.organization_id AND status='active')
  AND ((so.kind='organization' AND so.organization_id=fo.organization_id)
   OR (so.kind='platform' AND so.organization_id IS NULL AND EXISTS(
    SELECT 1 FROM organization_entitlements WHERE organization_id=fo.organization_id
    AND root_owner_id=so.id AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability'
    AND plugin_installation_id=src.installation_id AND status='active'))))))
)
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE id=p.binding_id AND source_key=p.source_key AND folder_id=p.folder_key)
 OR (SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=p.folder_key)<>1
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_scan_runs WHERE id=p.discovery_run_id AND source_key=p.source_key
  AND configuration_revision=p.configuration_revision AND state='complete')
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_entries WHERE source_key=p.source_key AND entry_id=p.entry_id
  AND revision=p.entry_revision AND logical_path=p.logical_path AND kind=p.entry_kind AND size=p.entry_size
  AND modified_unix_nano=p.entry_modified_unix_nano AND configuration_revision=p.configuration_revision
  AND last_seen_run=p.discovery_run_id AND absence_confirmed_at IS NULL)
 OR EXISTS(SELECT 1 FROM user_dropped_series WHERE series_id=p.item_key)
 OR EXISTS(SELECT 1 FROM watch_provider_dropped_items WHERE series_id=p.item_key)
 OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=p.item_key OR parent_id=p.item_key)
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
IF EXISTS(SELECT 1 FROM jsonb_array_elements(p.sidecar_shape) e WHERE NOT EXISTS(
 SELECT 1 FROM bloem_storage_entries s WHERE s.source_key=p.source_key AND s.entry_id=e->>'id'
 AND s.revision=e->>'revision' AND s.logical_path=e->>'logical_path'
 AND s.kind=(e->>'kind')::integer AND s.size=(e->>'size')::bigint AND s.modified_unix_nano=(e->>'modified_unix_nano')::bigint
 AND s.configuration_revision=p.configuration_revision AND s.last_seen_run=p.discovery_run_id AND s.absence_confirmed_at IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

SELECT * INTO saved FROM media_files WHERE file_path=p.catalog_location;
IF saved.id IS NULL OR saved.content_id IS DISTINCT FROM p.item_key OR saved.media_folder_id<>p.folder_key
 OR saved.episode_id IS NOT NULL OR saved.extra_id IS NOT NULL OR saved.missing_since IS NOT NULL
 OR saved.file_size<>p.entry_size OR saved.file_modified_at IS DISTINCT FROM p.normalized_modified_at
 OR saved.canonical_root_path<>p.catalog_location OR saved.observed_root_path<>p.catalog_location
 OR saved.content_group_key<>p.content_group_key OR saved.container<>p.parsed_container OR saved.probe_source<>'native'
 OR (p.retained_file_key IS NOT NULL AND saved.id<>p.retained_file_key)
 OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=p.item_key AND type='ebook')
 OR NOT EXISTS(SELECT 1 FROM media_item_libraries WHERE content_id=p.item_key AND media_folder_id=p.folder_key)
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=saved.id AND binding_id=p.binding_id
 AND entry_id=p.entry_id AND revision=p.entry_revision AND logical_path=p.logical_path AND configuration_revision=p.configuration_revision)
 OR bloem_native_item_class(p.item_key)<>'native'
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
SELECT jsonb_object_agg(key,to_jsonb(saved)->key) INTO shape FROM jsonb_each(p.file_shape);
IF shape IS DISTINCT FROM p.file_shape THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

 IF NOT EXISTS(SELECT 1 FROM bloem_storage_ingestion WHERE run_id=p.discovery_run_id AND binding_id=p.binding_id
  AND owner=p.ingestion_owner AND lease_epoch=p.ingestion_epoch AND last_entry_id=p.entry_id
  AND pending_entry_id='' AND pending_revision='' AND pending_token IS NULL)
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_folder_class(fid bigint) RETURNS text
    LANGUAGE plpgsql
    AS $$

DECLARE m bloem_native_libraries%ROWTYPE; f media_folders%ROWTYPE; n integer;
BEGIN
 IF fid IS NULL OR fid<=0 THEN RETURN 'inconsistent'; END IF;
 SELECT * INTO m FROM bloem_native_libraries WHERE folder_id=fid;
 SELECT count(*) INTO n FROM bloem_storage_bindings WHERE folder_id=fid;
 IF m.folder_id IS NULL THEN
  IF n<>0 THEN RETURN 'inconsistent'; END IF;
  RETURN 'local';
 END IF;
 SELECT * INTO f FROM media_folders WHERE id=fid;
 IF f.id IS NULL OR f.type<>'ebook' OR f.owner_id<>m.owner_id
 OR EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=fid)
 OR NOT EXISTS(SELECT 1 FROM resource_owners o WHERE o.id=m.owner_id AND o.revision>0
  AND ((o.kind='platform' AND o.organization_id IS NULL) OR (o.kind='organization' AND o.organization_id IS NOT NULL)))
 OR n>1 OR (n=1 AND (NOT m.initialized OR m.revision<>3))
 OR (n=0 AND m.revision=3) THEN RETURN 'inconsistent'; END IF;
 RETURN 'native';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_binding() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE m bloem_native_libraries%ROWTYPE; f media_folders%ROWTYPE; src bloem_storage_sources%ROWTYPE; fid bigint;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  IF bloem_native_folder_class(NEW.folder_id)<>'native' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  RETURN NEW;
 END IF;
 fid:=NEW.folder_id;
 SELECT * INTO f FROM media_folders WHERE id=fid FOR UPDATE NOWAIT;
 SELECT * INTO m FROM bloem_native_libraries WHERE folder_id=fid FOR UPDATE NOWAIT;
 SELECT * INTO src FROM bloem_storage_sources WHERE key=NEW.source_key FOR SHARE NOWAIT;
 IF m.folder_id IS NULL OR NOT m.initialized OR m.revision<>2 OR m.deleting_job_id IS NOT NULL
 OR f.id IS NULL OR f.type<>'ebook' OR NOT f.enabled OR f.owner_id<>m.owner_id
 OR src.key IS NULL OR NOT src.enabled OR src.installation_id IS NULL
 OR NOT EXISTS(SELECT 1 FROM plugin_installations i JOIN bloem_storage_installations ni ON ni.installation_id=i.id
  WHERE i.id=src.installation_id AND i.enabled AND i.kind='plugin' AND i.plugin_id=src.plugin_id
  AND i.owner_id=src.owner_id AND ni.owner_id=i.owner_id AND ni.protocol_version=1 AND i.runtime_generation>0)
 OR NOT (
EXISTS(SELECT 1 FROM resource_owners fo JOIN resource_owners so ON so.id=src.owner_id
 WHERE fo.id=f.owner_id AND fo.revision>0 AND so.revision>0
 AND ((fo.kind='platform' AND fo.organization_id IS NULL AND so.kind='platform' AND so.organization_id IS NULL)
 OR (fo.kind='organization' AND fo.organization_id IS NOT NULL
  AND EXISTS(SELECT 1 FROM organizations WHERE id=fo.organization_id AND status='active')
  AND ((so.kind='organization' AND so.organization_id=fo.organization_id)
   OR (so.kind='platform' AND so.organization_id IS NULL AND EXISTS(
    SELECT 1 FROM organization_entitlements WHERE organization_id=fo.organization_id
    AND root_owner_id=so.id AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability'
    AND plugin_installation_id=src.installation_id AND status='active'))))))
)
 OR EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=fid)
 OR EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=fid)
 OR EXISTS(SELECT 1 FROM media_files WHERE media_folder_id=fid)
 OR EXISTS(SELECT 1 FROM media_item_libraries WHERE media_folder_id=fid)
 OR EXISTS(SELECT 1 FROM episode_libraries WHERE media_folder_id=fid)
 THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 IF EXISTS(SELECT 1 FROM media_item_roots WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM media_item_groups WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM scanned_media_roots WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM scanned_media_groups WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM media_group_locations WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM observed_media_locations WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM media_group_overrides WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM skipped_media_roots WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM media_root_overrides WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
IF EXISTS(SELECT 1 FROM series_root_match_queue WHERE media_folder_id=fid) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;

 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_book_drop() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE keys text[]:=ARRAY[NEW.series_id]; k text; same boolean:=TG_OP='INSERT';
BEGIN
 IF TG_OP='UPDATE' THEN keys:=keys||ARRAY[OLD.series_id]; same:=OLD.series_id=NEW.series_id; END IF;
 -- Every event reclassifies; a same-key timestamp/provider update is protected.
 FOREACH k IN ARRAY keys LOOP
  IF bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  IF NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=k) THEN same:=false; END IF;
 END LOOP;
 IF same THEN RETURN NEW; END IF;
 PERFORM bloem_native_lock_item_keys(keys);
 FOREACH k IN ARRAY keys LOOP
  IF bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 END LOOP;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_extra() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE s media_extras%ROWTYPE; keys text[]:=ARRAY[NEW.content_id,NEW.parent_id]; k text; same boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  SELECT * INTO s FROM media_extras WHERE content_id=NEW.content_id FOR NO KEY UPDATE;
  same:=FOUND AND s.parent_id=NEW.parent_id;
 ELSE s:=OLD; same:=OLD.content_id=NEW.content_id AND OLD.parent_id=NEW.parent_id;
 END IF;
 keys:=keys||ARRAY[s.content_id,s.parent_id];
 IF NEW.content_id=NEW.parent_id
 OR EXISTS(SELECT 1 FROM media_items WHERE content_id=NEW.content_id)
 OR EXISTS(SELECT 1 FROM seasons WHERE content_id=NEW.content_id OR content_id=NEW.parent_id)
 OR EXISTS(SELECT 1 FROM episodes WHERE content_id=NEW.content_id OR content_id=NEW.parent_id)
 OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=NEW.parent_id)
 OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=NEW.parent_id)
 THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 FOREACH k IN ARRAY keys LOOP IF bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF; END LOOP;
 IF same THEN RETURN NEW; END IF;
 PERFORM bloem_native_lock_item_keys(keys);
 FOREACH k IN ARRAY keys LOOP IF bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF; END LOOP;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_file() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE; s media_files%ROWTYPE; e media_files%ROWTYPE;
 shape jsonb; k text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT (bloem_native_folder_class(OLD.media_folder_id)='local' AND bloem_native_item_class(OLD.content_id)='local' AND bloem_native_item_class(OLD.episode_id)='local' AND bloem_native_item_class(OLD.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=OLD.id)) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO p FROM bloem_native_publication_permits WHERE xid=txid_current();
 IF p.xid IS NOT NULL AND NEW.content_id=p.item_key AND NEW.media_folder_id=p.folder_key AND NEW.file_path=p.catalog_location THEN
  IF NOT p.item_checked OR NOT p.member_checked OR NEW.episode_id IS NOT NULL OR NEW.extra_id IS NOT NULL
  OR NEW.missing_since IS NOT NULL OR NEW.canonical_root_path<>p.catalog_location OR NEW.observed_root_path<>p.catalog_location
  OR NEW.container<>p.parsed_container OR NEW.file_modified_at IS DISTINCT FROM p.normalized_modified_at
  OR NEW.file_size<>p.entry_size OR NEW.content_group_key<>p.content_group_key OR NEW.probe_source<>'native'
  THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  SELECT jsonb_object_agg(key,to_jsonb(NEW)->key) INTO shape FROM jsonb_each(p.file_shape);
  -- Compare the canonical conflict result without rewriting incoming NEW.
  -- The repository retains stored first-seen provenance on a rescan.
  IF TG_OP='INSERT' AND p.retained_file_key IS NOT NULL THEN
   shape:=jsonb_set(shape,'{first_seen_scan_run_id}',p.file_shape->'first_seen_scan_run_id');
  END IF;
  IF shape IS DISTINCT FROM p.file_shape THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  IF TG_OP='INSERT' THEN
   IF p.phase<>'member' OR p.attempted_file_key IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
   UPDATE bloem_native_publication_permits SET phase='file',attempted_file_key=NEW.id WHERE xid=p.xid;
  ELSE
   IF p.phase<>'file' OR p.attempted_file_key IS NULL OR p.retained_file_key IS DISTINCT FROM OLD.id
   OR ROW(OLD.id,OLD.content_id,OLD.episode_id,OLD.extra_id,OLD.media_folder_id,OLD.file_path) IS DISTINCT FROM ROW(NEW.id,NEW.content_id,NEW.episode_id,NEW.extra_id,NEW.media_folder_id,NEW.file_path)
   THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  END IF;
  RETURN NEW;
 END IF;
 IF TG_OP='INSERT' THEN
  -- Keep this ordinary row wait before all new key/parent coordination.
  SELECT * INTO s FROM media_files WHERE file_path=NEW.file_path FOR NO KEY UPDATE;
  IF FOUND THEN
   e:=NEW; e.id:=s.id; e.file_path:=s.file_path;
   e.content_id:=CASE WHEN NEW.extra_id IS NOT NULL THEN NULL ELSE COALESCE(NEW.content_id,s.content_id) END;
   e.episode_id:=CASE WHEN NEW.extra_id IS NOT NULL THEN NULL ELSE COALESCE(NEW.episode_id,s.episode_id) END;
   IF ROW(e.id,e.content_id,e.episode_id,e.extra_id,e.media_folder_id,e.file_path) IS NOT DISTINCT FROM ROW(s.id,s.content_id,s.episode_id,s.extra_id,s.media_folder_id,s.file_path) AND bloem_native_folder_class(s.media_folder_id)='local' AND bloem_native_item_class(s.content_id)='local' AND bloem_native_item_class(s.episode_id)='local' AND bloem_native_item_class(s.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=s.id) AND bloem_native_folder_class(e.media_folder_id)='local' AND bloem_native_item_class(e.content_id)='local' AND bloem_native_item_class(e.episode_id)='local' AND bloem_native_item_class(e.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=e.id)
   THEN RETURN NEW; END IF;
  END IF;
 ELSE
  IF ROW(OLD.id,OLD.content_id,OLD.episode_id,OLD.extra_id,OLD.media_folder_id,OLD.file_path) IS NOT DISTINCT FROM ROW(NEW.id,NEW.content_id,NEW.episode_id,NEW.extra_id,NEW.media_folder_id,NEW.file_path) AND bloem_native_folder_class(OLD.media_folder_id)='local' AND bloem_native_item_class(OLD.content_id)='local' AND bloem_native_item_class(OLD.episode_id)='local' AND bloem_native_item_class(OLD.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=OLD.id) AND bloem_native_folder_class(NEW.media_folder_id)='local' AND bloem_native_item_class(NEW.content_id)='local' AND bloem_native_item_class(NEW.episode_id)='local' AND bloem_native_item_class(NEW.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=NEW.id)
  THEN RETURN NEW; END IF;
  s:=OLD; e:=NEW;
 END IF;
 -- FK SET NULL executes after an ordinary extra row has been deleted.
 -- Its missing own-key alias cannot classify OLD; admit only this exact
 -- nested cascade, with unchanged other identity and wholly local evidence.
 IF TG_OP='UPDATE' AND pg_trigger_depth()>1 AND OLD.extra_id IS NOT NULL AND NEW.extra_id IS NULL
 AND ROW(OLD.id,OLD.content_id,OLD.episode_id,OLD.media_folder_id,OLD.file_path)
 IS NOT DISTINCT FROM ROW(NEW.id,NEW.content_id,NEW.episode_id,NEW.media_folder_id,NEW.file_path)
 AND NOT EXISTS(SELECT 1 FROM media_extras WHERE content_id=OLD.extra_id)
 AND NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=OLD.extra_id)
 AND NOT EXISTS(SELECT 1 FROM episodes WHERE content_id=OLD.extra_id)
 AND NOT EXISTS(SELECT 1 FROM seasons WHERE content_id=OLD.extra_id)
 AND bloem_native_folder_class(NEW.media_folder_id)='local' AND bloem_native_item_class(NEW.content_id)='local' AND bloem_native_item_class(NEW.episode_id)='local' AND bloem_native_item_class(NEW.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=NEW.id)
 AND NOT EXISTS(SELECT 1 FROM media_files alias WHERE alias.extra_id=OLD.extra_id
  AND (bloem_native_folder_class(alias.media_folder_id)<>'local'
   OR bloem_native_item_class(alias.content_id)<>'local'
   OR bloem_native_item_class(alias.episode_id)<>'local'
   OR EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=alias.id)))
 THEN RETURN NEW; END IF;
 PERFORM bloem_native_lock_item_keys(ARRAY[NEW.content_id,NEW.episode_id,NEW.extra_id,s.content_id,s.episode_id,s.extra_id]);
 IF NOT (bloem_native_folder_class(NEW.media_folder_id)='local' AND bloem_native_item_class(NEW.content_id)='local' AND bloem_native_item_class(NEW.episode_id)='local' AND bloem_native_item_class(NEW.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=NEW.id)) OR (s.id IS NOT NULL AND NOT (bloem_native_folder_class(s.media_folder_id)='local' AND bloem_native_item_class(s.content_id)='local' AND bloem_native_item_class(s.episode_id)='local' AND bloem_native_item_class(s.extra_id)='local' AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=s.id))) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 PERFORM id FROM media_folders WHERE id IN(NEW.media_folder_id,s.media_folder_id) ORDER BY id FOR SHARE NOWAIT;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_folder_changed() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

BEGIN
 IF (to_jsonb(OLD)-ARRAY['last_scanned_at','scan_warning_code','scan_warning_message','scan_warning_at'])
 IS DISTINCT FROM (to_jsonb(NEW)-ARRAY['last_scanned_at','scan_warning_code','scan_warning_message','scan_warning_at'])
 AND bloem_native_folder_class(OLD.id)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_folder_child() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE o jsonb:=CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 n jsonb:=CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 fid bigint; k text; keys text[]; v text;
BEGIN
 keys:=ARRAY[o->>'content_id',n->>'content_id'];
 IF TG_OP<>'DELETE' AND (TG_OP='INSERT' OR o->TG_ARGV[0] IS DISTINCT FROM n->TG_ARGV[0] OR o->'content_id' IS DISTINCT FROM n->'content_id') THEN
  PERFORM bloem_native_lock_item_keys(keys);
 END IF;
 FOREACH v IN ARRAY ARRAY[o->>TG_ARGV[0],n->>TG_ARGV[0]] LOOP
  IF v IS NOT NULL THEN
   fid:=v::bigint;
   IF bloem_native_folder_class(fid)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
   IF TG_OP<>'DELETE' THEN PERFORM id FROM media_folders WHERE id=fid FOR SHARE NOWAIT; END IF;
  END IF;
 END LOOP;
 FOREACH k IN ARRAY keys LOOP
  IF bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_folder_protected() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

BEGIN
 IF bloem_native_folder_class(OLD.id)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 IF TG_OP='UPDATE' AND NEW.id IS DISTINCT FROM OLD.id THEN
  IF bloem_native_folder_class(NEW.id)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_identity() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE col text; keys text[]:=ARRAY[]::text[]; o jsonb:=to_jsonb(OLD); n jsonb:=to_jsonb(NEW);
BEGIN
 FOREACH col IN ARRAY TG_ARGV LOOP
  IF o->col IS DISTINCT FROM n->col THEN
   IF col='item_type' THEN keys:=keys||ARRAY[o->>'content_id',n->>'content_id'];
   ELSE keys:=keys||ARRAY[o->>col,n->>col]; END IF;
  END IF;
 END LOOP;
 IF cardinality(keys)=0 THEN RETURN NEW; END IF;
 PERFORM bloem_native_lock_item_keys(keys);
 FOREACH col IN ARRAY keys LOOP
  IF bloem_native_item_class(col)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 END LOOP;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_item() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE; c text;
BEGIN
 IF TG_OP='DELETE' THEN
  IF current_setting('bloem.native_provider_target_xid',true)=txid_current()::text
  OR bloem_native_item_class(OLD.content_id)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  RETURN OLD;
 END IF;
 SELECT * INTO p FROM bloem_native_publication_permits WHERE xid=txid_current();
 IF p.xid IS NOT NULL AND p.item_key=NEW.content_id AND p.phase='armed' THEN
  IF NEW.type<>'ebook' OR (TG_OP='UPDATE' AND (OLD.content_id IS DISTINCT FROM NEW.content_id
   OR OLD.type IS DISTINCT FROM NEW.type OR p.existing_item_key IS DISTINCT FROM OLD.content_id
   OR p.item_version IS DISTINCT FROM OLD.updated_at)) THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  UPDATE bloem_native_publication_permits SET phase='item',item_checked=true WHERE xid=p.xid;
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND OLD.content_id=NEW.content_id AND OLD.type=NEW.type THEN
  c:=bloem_native_item_class(OLD.content_id);
  IF c='inconsistent' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  IF c='native' THEN PERFORM set_config('bloem.native_provider_target_xid',txid_current()::text,true); END IF;
  RETURN NEW;
 END IF;
 PERFORM bloem_native_lock_item_keys(ARRAY[NEW.content_id,CASE WHEN TG_OP='UPDATE' THEN OLD.content_id END]);
 IF bloem_native_item_class(NEW.content_id)<>'local'
 OR (TG_OP='UPDATE' AND bloem_native_item_class(OLD.content_id)<>'local') THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_marker() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE fid bigint:=NEW.folder_id; f media_folders%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 SELECT * INTO f FROM media_folders WHERE id=fid FOR UPDATE NOWAIT;
 IF f.id IS NULL OR f.type<>'ebook' OR NOT f.enabled OR f.owner_id<>NEW.owner_id
 OR EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=fid)
 OR NEW.deleting_job_id IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.initialized OR NEW.revision<>1
  OR NOT EXISTS(SELECT 1 FROM media_folders WHERE id=fid AND xmin::text::bigint=txid_current()%4294967296)
  OR EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=fid)
  OR EXISTS(SELECT 1 FROM media_files WHERE media_folder_id=fid)
  OR EXISTS(SELECT 1 FROM media_item_libraries WHERE media_folder_id=fid)
  THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  RETURN NEW;
 END IF;
 IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 IF (to_jsonb(NEW)-ARRAY['revision','initialized']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['revision','initialized']) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 IF NOT OLD.initialized AND OLD.revision=1 AND NEW.initialized AND NEW.revision=2 THEN
  PERFORM id FROM page_sections WHERE (scope='library' AND library_id=fid)
   OR (scope='home' AND config->>'generated_source'='home_library_recent'
    AND (config->>'generated_library_id'=fid::text OR config->>'filter_library_id'=fid::text))
   ORDER BY id FOR SHARE NOWAIT;
  PERFORM section_id FROM page_section_revisions WHERE section_id IN(
   SELECT id FROM page_sections WHERE scope='library' AND library_id=fid
   OR scope='home' AND config->>'generated_source'='home_library_recent'
    AND (config->>'generated_library_id'=fid::text OR config->>'filter_library_id'=fid::text))
   ORDER BY section_id FOR SHARE NOWAIT;
  PERFORM scope FROM page_section_scope_revisions WHERE (scope='library' AND library_id=fid)
   OR (scope='home' AND library_id=0) ORDER BY scope,library_id FOR SHARE NOWAIT;
  PERFORM id FROM library_collection_groups WHERE library_id=fid ORDER BY id FOR SHARE NOWAIT;
  PERFORM library_id FROM library_collection_order_revisions WHERE library_id=fid FOR SHARE NOWAIT;
  IF EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=fid) OR NOT (
EXISTS(SELECT 1 FROM library_collection_groups g JOIN library_collection_order_revisions r ON r.library_id=g.library_id
 WHERE g.library_id=fid AND g.id='lcg_user_'||fid::text AND g.kind='user_collections' AND g.label='user-collections' AND r.revision>0)
AND EXISTS(SELECT 1 FROM page_sections s JOIN page_section_revisions r ON r.section_id=s.id
 JOIN page_section_scope_revisions sr ON sr.scope=s.scope AND sr.library_id=s.library_id
 WHERE s.scope='library' AND s.library_id=fid AND s.enabled AND r.revision>0 AND sr.revision>0)
AND NOT EXISTS(SELECT 1 FROM page_sections s LEFT JOIN page_section_revisions r ON r.section_id=s.id
 WHERE s.scope='library' AND s.library_id=fid AND (r.revision IS NULL OR r.revision<=0))
AND NOT EXISTS(SELECT 1 FROM (VALUES('recently_added'),('recently_released')) k(kind)
 WHERE NOT EXISTS(SELECT 1 FROM page_sections s JOIN page_section_revisions r ON r.section_id=s.id
 JOIN page_section_scope_revisions sr ON sr.scope='home' AND sr.library_id=0
 WHERE s.scope='home' AND s.enabled AND s.section_type=k.kind AND r.revision>0 AND sr.revision>0
 AND s.config->>'generated_source'='home_library_recent'
 AND (s.config->>'generated_library_id'=fid::text OR s.config->>'filter_library_id'=fid::text)))
) THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.initialized AND OLD.revision=2 AND NEW.initialized AND NEW.revision=3
 AND (SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=fid)=1
 AND EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE folder_id=fid AND xmin::text::bigint=txid_current()%4294967296)
 THEN RETURN NEW; END IF;
 RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_member() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE; same boolean:=false; k text; fid bigint;
BEGIN
 IF TG_OP<>'DELETE' THEN
  SELECT * INTO p FROM bloem_native_publication_permits WHERE xid=txid_current();
  IF p.xid IS NOT NULL AND p.item_key=NEW.content_id AND p.folder_key=NEW.media_folder_id
  AND p.item_checked AND p.phase IN('item','member') THEN
   IF TG_OP='UPDATE' AND (OLD.content_id<>NEW.content_id OR OLD.media_folder_id<>NEW.media_folder_id) THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
   UPDATE bloem_native_publication_permits SET phase='member',member_checked=true WHERE xid=p.xid;
   RETURN NEW;
  END IF;
  IF TG_OP='INSERT' THEN
   PERFORM 1 FROM media_item_libraries WHERE content_id=NEW.content_id AND media_folder_id=NEW.media_folder_id FOR NO KEY UPDATE;
   same:=FOUND;
  ELSE same:=OLD.content_id=NEW.content_id AND OLD.media_folder_id=NEW.media_folder_id;
  END IF;
 END IF;
 IF TG_OP='DELETE' OR same THEN
  k:=CASE WHEN TG_OP='DELETE' THEN OLD.content_id ELSE NEW.content_id END;
  fid:=CASE WHEN TG_OP='DELETE' THEN OLD.media_folder_id ELSE NEW.media_folder_id END;
  IF bloem_native_folder_class(fid)<>'local' OR bloem_native_item_class(k)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
  IF TG_OP='DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
 END IF;
 PERFORM bloem_native_lock_item_keys(ARRAY[NEW.content_id,CASE WHEN TG_OP='UPDATE' THEN OLD.content_id END]);
 IF bloem_native_folder_class(NEW.media_folder_id)<>'local' OR bloem_native_item_class(NEW.content_id)<>'local'
 OR (TG_OP='UPDATE' AND (bloem_native_folder_class(OLD.media_folder_id)<>'local' OR bloem_native_item_class(OLD.content_id)<>'local'))
 THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 PERFORM id FROM media_folders WHERE id IN(NEW.media_folder_id,CASE WHEN TG_OP='UPDATE' THEN OLD.media_folder_id END) ORDER BY id FOR SHARE NOWAIT;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_permit() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE; m bloem_native_libraries%ROWTYPE; f media_folders%ROWTYPE;
 src bloem_storage_sources%ROWTYPE; saved media_files%ROWTYPE; shape jsonb;
BEGIN
 IF current_setting('transaction_isolation') <> 'read committed' THEN
 RAISE EXCEPTION USING ERRCODE='BN003', MESSAGE='Native storage admission unavailable';
END IF;
 IF TG_OP='DELETE' THEN
  IF NOT OLD.finished OR OLD.phase<>'finished' THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  RETURN OLD;
 END IF;
 p:=NEW;
 IF p.xid<>txid_current() THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

SELECT * INTO m FROM bloem_native_libraries WHERE folder_id=p.folder_key;
SELECT * INTO f FROM media_folders WHERE id=p.folder_key;
SELECT * INTO src FROM bloem_storage_sources WHERE key=p.source_key;
IF m.folder_id IS NULL OR NOT m.initialized OR m.revision<>p.library_revision OR m.revision<>3
 OR m.deleting_job_id IS NOT NULL OR m.owner_id<>p.folder_owner_id OR f.owner_id<>m.owner_id OR NOT f.enabled OR f.type<>'ebook'
 OR EXISTS(SELECT 1 FROM media_folder_paths WHERE media_folder_id=p.folder_key)
 OR src.key IS NULL OR NOT src.enabled OR src.owner_id<>p.source_owner_id OR src.installation_id IS DISTINCT FROM p.installation_key
 OR src.configuration_revision<>p.configuration_revision OR src.discovery_run_id IS DISTINCT FROM p.discovery_run_id
 OR NOT EXISTS(SELECT 1 FROM plugin_installations i JOIN bloem_storage_installations ni ON ni.installation_id=i.id
  WHERE i.id=p.installation_key AND i.owner_id=p.source_owner_id AND i.enabled AND i.kind='plugin'
  AND i.plugin_id=src.plugin_id AND i.runtime_generation=p.runtime_generation
  AND ni.owner_id=i.owner_id AND ni.protocol_version=p.protocol_version AND p.protocol_version=1)
 OR NOT (
EXISTS(SELECT 1 FROM resource_owners fo JOIN resource_owners so ON so.id=src.owner_id
 WHERE fo.id=f.owner_id AND fo.revision>0 AND so.revision>0
 AND ((fo.kind='platform' AND fo.organization_id IS NULL AND so.kind='platform' AND so.organization_id IS NULL)
 OR (fo.kind='organization' AND fo.organization_id IS NOT NULL
  AND EXISTS(SELECT 1 FROM organizations WHERE id=fo.organization_id AND status='active')
  AND ((so.kind='organization' AND so.organization_id=fo.organization_id)
   OR (so.kind='platform' AND so.organization_id IS NULL AND EXISTS(
    SELECT 1 FROM organization_entitlements WHERE organization_id=fo.organization_id
    AND root_owner_id=so.id AND root_kind='plugin_installation' AND entitlement_kind='plugin_availability'
    AND plugin_installation_id=src.installation_id AND status='active'))))))
)
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_bindings WHERE id=p.binding_id AND source_key=p.source_key AND folder_id=p.folder_key)
 OR (SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=p.folder_key)<>1
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_scan_runs WHERE id=p.discovery_run_id AND source_key=p.source_key
  AND configuration_revision=p.configuration_revision AND state='complete')
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_entries WHERE source_key=p.source_key AND entry_id=p.entry_id
  AND revision=p.entry_revision AND logical_path=p.logical_path AND kind=p.entry_kind AND size=p.entry_size
  AND modified_unix_nano=p.entry_modified_unix_nano AND configuration_revision=p.configuration_revision
  AND last_seen_run=p.discovery_run_id AND absence_confirmed_at IS NULL)
 OR EXISTS(SELECT 1 FROM user_dropped_series WHERE series_id=p.item_key)
 OR EXISTS(SELECT 1 FROM watch_provider_dropped_items WHERE series_id=p.item_key)
 OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=p.item_key OR parent_id=p.item_key)
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
IF EXISTS(SELECT 1 FROM jsonb_array_elements(p.sidecar_shape) e WHERE NOT EXISTS(
 SELECT 1 FROM bloem_storage_entries s WHERE s.source_key=p.source_key AND s.entry_id=e->>'id'
 AND s.revision=e->>'revision' AND s.logical_path=e->>'logical_path'
 AND s.kind=(e->>'kind')::integer AND s.size=(e->>'size')::bigint AND s.modified_unix_nano=(e->>'modified_unix_nano')::bigint
 AND s.configuration_revision=p.configuration_revision AND s.last_seen_run=p.discovery_run_id AND s.absence_confirmed_at IS NULL))
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

IF NOT EXISTS(SELECT 1 FROM bloem_storage_ingestion WHERE run_id=p.discovery_run_id AND binding_id=p.binding_id
 AND owner=p.ingestion_owner AND lease_epoch=p.ingestion_epoch AND pending_token=p.lease_token
 AND pending_entry_id=p.entry_id AND pending_revision=p.entry_revision AND NOT complete AND lease_until>clock_timestamp())
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

 IF TG_OP='INSERT' THEN
  IF p.phase<>'armed' OR p.item_checked OR p.member_checked OR p.ref_checked OR p.finished
   OR p.attempted_file_key IS NOT NULL OR p.stored_file_key IS NOT NULL THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(OLD)-ARRAY['phase','item_checked','member_checked','ref_checked','finished','attempted_file_key','stored_file_key'])
 IS DISTINCT FROM (to_jsonb(NEW)-ARRAY['phase','item_checked','member_checked','ref_checked','finished','attempted_file_key','stored_file_key'])
 OR (OLD.item_checked AND NOT NEW.item_checked) OR (OLD.member_checked AND NOT NEW.member_checked)
 OR (OLD.ref_checked AND NOT NEW.ref_checked) OR (OLD.finished AND NOT NEW.finished)
 OR (OLD.attempted_file_key IS NOT NULL AND OLD.attempted_file_key IS DISTINCT FROM NEW.attempted_file_key)
 OR (OLD.stored_file_key IS NOT NULL AND OLD.stored_file_key IS DISTINCT FROM NEW.stored_file_key)
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
 IF OLD.phase='armed' AND NEW.phase='item' AND NEW.item_checked AND NOT NEW.member_checked
 AND NOT NEW.ref_checked AND NOT NEW.finished AND NEW.attempted_file_key IS NULL AND NEW.stored_file_key IS NULL THEN RETURN NEW; END IF;
 IF OLD.phase IN('item','member') AND NEW.phase='member' AND NEW.item_checked AND NEW.member_checked
 AND NOT NEW.ref_checked AND NOT NEW.finished AND NEW.attempted_file_key IS NULL AND NEW.stored_file_key IS NULL THEN RETURN NEW; END IF;
 IF OLD.phase='member' AND NEW.phase='file' AND NEW.item_checked AND NEW.member_checked
 AND NOT NEW.ref_checked AND NOT NEW.finished AND NEW.attempted_file_key IS NOT NULL AND NEW.stored_file_key IS NULL THEN RETURN NEW; END IF;
 IF OLD.phase='file' AND NEW.phase='file' AND NEW.item_checked AND NEW.member_checked
 AND NOT NEW.ref_checked AND NOT NEW.finished AND NEW.stored_file_key IS NOT NULL
 AND NEW.stored_file_key=COALESCE(NEW.retained_file_key,NEW.attempted_file_key) THEN
  SELECT * INTO saved FROM media_files WHERE id=NEW.stored_file_key;
  IF saved.id IS NULL OR saved.file_path<>p.catalog_location OR saved.content_id IS DISTINCT FROM p.item_key
  OR saved.media_folder_id<>p.folder_key THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  SELECT jsonb_object_agg(key,to_jsonb(saved)->key) INTO shape FROM jsonb_each(p.file_shape);
  IF shape IS DISTINCT FROM p.file_shape THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.phase IN('file','ref') AND NEW.phase='ref' AND NEW.item_checked AND NEW.member_checked
 AND NEW.ref_checked AND NOT NEW.finished AND OLD.stored_file_key IS NOT NULL
 AND NEW.stored_file_key=OLD.stored_file_key THEN RETURN NEW; END IF;
 IF OLD.phase='ref' AND NEW.phase='finished' AND NEW.item_checked AND NEW.member_checked AND NEW.ref_checked AND NEW.finished THEN

SELECT * INTO saved FROM media_files WHERE file_path=p.catalog_location;
IF saved.id IS NULL OR saved.content_id IS DISTINCT FROM p.item_key OR saved.media_folder_id<>p.folder_key
 OR saved.episode_id IS NOT NULL OR saved.extra_id IS NOT NULL OR saved.missing_since IS NOT NULL
 OR saved.file_size<>p.entry_size OR saved.file_modified_at IS DISTINCT FROM p.normalized_modified_at
 OR saved.canonical_root_path<>p.catalog_location OR saved.observed_root_path<>p.catalog_location
 OR saved.content_group_key<>p.content_group_key OR saved.container<>p.parsed_container OR saved.probe_source<>'native'
 OR (p.retained_file_key IS NOT NULL AND saved.id<>p.retained_file_key)
 OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=p.item_key AND type='ebook')
 OR NOT EXISTS(SELECT 1 FROM media_item_libraries WHERE content_id=p.item_key AND media_folder_id=p.folder_key)
 OR NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs WHERE media_file_id=saved.id AND binding_id=p.binding_id
 AND entry_id=p.entry_id AND revision=p.entry_revision AND logical_path=p.logical_path AND configuration_revision=p.configuration_revision)
 OR bloem_native_item_class(p.item_key)<>'native'
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
SELECT jsonb_object_agg(key,to_jsonb(saved)->key) INTO shape FROM jsonb_each(p.file_shape);
IF shape IS DISTINCT FROM p.file_shape THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;

  IF saved.id<>NEW.stored_file_key THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
  RETURN NEW;
 END IF;
 RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_ref() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE p bloem_native_publication_permits%ROWTYPE;
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 SELECT * INTO p FROM bloem_native_publication_permits WHERE xid=txid_current();
 IF p.xid IS NULL OR p.phase NOT IN('file','ref') OR p.stored_file_key IS DISTINCT FROM NEW.media_file_id
 OR NOT p.item_checked OR NOT p.member_checked OR p.binding_id<>NEW.binding_id OR p.entry_id<>NEW.entry_id
 OR p.entry_revision<>NEW.revision OR p.logical_path<>NEW.logical_path OR p.configuration_revision<>NEW.configuration_revision
 OR (TG_OP='UPDATE' AND (OLD.media_file_id<>NEW.media_file_id OR OLD.binding_id<>NEW.binding_id OR OLD.entry_id<>NEW.entry_id))
 OR NOT EXISTS(SELECT 1 FROM media_files WHERE id=NEW.media_file_id AND content_id=p.item_key
 AND media_folder_id=p.folder_key AND file_path=p.catalog_location)
 THEN RAISE EXCEPTION USING ERRCODE='BN002', MESSAGE='Native publication unavailable'; END IF;
 UPDATE bloem_native_publication_permits SET phase='ref',ref_checked=true WHERE xid=p.xid;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_guard_video() RETURNS trigger
    LANGUAGE plpgsql
    AS $$

DECLARE o jsonb:=CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END; n jsonb:=to_jsonb(NEW);
 col text; keys text[]:=ARRAY[]::text[]; v text; same boolean:=TG_OP='UPDATE';
BEGIN
 FOREACH col IN ARRAY TG_ARGV LOOP
  IF o->col IS DISTINCT FROM n->col THEN same:=false; END IF;
  IF col<>'media_folder_id' THEN keys:=keys||ARRAY[o->>col,n->>col]; END IF;
 END LOOP;
 IF NOT same THEN PERFORM bloem_native_lock_item_keys(keys); END IF;
 FOREACH v IN ARRAY keys LOOP
  IF bloem_native_item_class(v)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
 END LOOP;
 FOREACH v IN ARRAY ARRAY[o->>'media_folder_id',n->>'media_folder_id'] LOOP
  IF v IS NOT NULL THEN
   IF bloem_native_folder_class(v::bigint)<>'local' THEN RAISE EXCEPTION USING ERRCODE='BN001', MESSAGE='Native local operation unsupported'; END IF;
   IF NOT same THEN PERFORM id FROM media_folders WHERE id=v::bigint FOR SHARE NOWAIT; END IF;
  END IF;
 END LOOP;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_item_class(supplied text) RETURNS text
    LANGUAGE plpgsql
    AS $$

DECLARE keys text[]; k text; parent text; series text; season text;
 roles integer; c text; b uuid; native_binding uuid; native_seen boolean:=false; local_seen boolean:=false;
 rec record;
BEGIN
 IF supplied IS NULL OR supplied='' THEN RETURN 'local'; END IF;
 keys:=ARRAY[supplied];
 SELECT (EXISTS(SELECT 1 FROM media_items WHERE content_id=supplied))::int+
 (EXISTS(SELECT 1 FROM media_extras WHERE content_id=supplied))::int+
 (EXISTS(SELECT 1 FROM episodes WHERE content_id=supplied))::int+
 (EXISTS(SELECT 1 FROM seasons WHERE content_id=supplied))::int INTO roles;
 IF roles>1 OR (roles=0 AND (EXISTS(SELECT 1 FROM media_files WHERE episode_id=supplied OR extra_id=supplied)
 OR EXISTS(SELECT 1 FROM episode_libraries WHERE episode_id=supplied))) THEN RETURN 'inconsistent'; END IF;
 SELECT parent_id INTO parent FROM media_extras WHERE content_id=supplied;
 IF FOUND THEN
  IF parent=supplied OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=parent)
  OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=parent)
  OR EXISTS(SELECT 1 FROM episodes WHERE content_id=parent)
  OR EXISTS(SELECT 1 FROM seasons WHERE content_id=parent) THEN RETURN 'inconsistent'; END IF;
  keys:=array_append(keys,parent);
 END IF;
 SELECT series_id,season_id INTO series,season FROM episodes WHERE content_id=supplied;
 IF FOUND THEN
  -- Local podcast and TV episodes share the required parent edge. A
  -- season is optional; when supplied it must resolve to that same parent.
  IF series=supplied
  OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=series AND type IN('series','podcast'))
  OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=series)
  OR EXISTS(SELECT 1 FROM episodes WHERE content_id=series)
  OR EXISTS(SELECT 1 FROM seasons WHERE content_id=series) THEN RETURN 'inconsistent'; END IF;
  keys:=array_append(keys,series);
  IF season IS NOT NULL THEN
   IF season=supplied OR season=series
   OR NOT EXISTS(SELECT 1 FROM seasons WHERE content_id=season AND series_id=series)
   OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=season)
   OR EXISTS(SELECT 1 FROM episodes WHERE content_id=season)
   OR EXISTS(SELECT 1 FROM media_items WHERE content_id=season) THEN RETURN 'inconsistent'; END IF;
   keys:=array_append(keys,season);
  END IF;
 ELSE
  SELECT series_id INTO series FROM seasons WHERE content_id=supplied;
  IF FOUND THEN
   IF series=supplied OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=series AND type IN('series','podcast'))
   OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=series)
   OR EXISTS(SELECT 1 FROM episodes WHERE content_id=series)
   OR EXISTS(SELECT 1 FROM seasons WHERE content_id=series) THEN RETURN 'inconsistent'; END IF;
   keys:=array_append(keys,series);
  END IF;
 END IF;
 FOREACH k IN ARRAY keys LOOP
  FOR rec IN
   SELECT f.id,f.media_folder_id,f.content_id,f.episode_id,f.extra_id,r.binding_id,b.folder_id AS binding_folder
   FROM media_files f LEFT JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
   LEFT JOIN bloem_storage_bindings b ON b.id=r.binding_id
   WHERE f.content_id=k OR f.episode_id=k OR f.extra_id=k
  LOOP
   c:=bloem_native_folder_class(rec.media_folder_id);
   IF c='inconsistent' THEN RETURN c; END IF;
   IF c='native' THEN
    IF rec.binding_id IS NULL OR rec.binding_folder IS DISTINCT FROM rec.media_folder_id
    OR rec.content_id IS NULL OR rec.episode_id IS NOT NULL OR rec.extra_id IS NOT NULL
    OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=rec.content_id AND type='ebook')
    OR NOT EXISTS(SELECT 1 FROM media_item_libraries WHERE content_id=rec.content_id AND media_folder_id=rec.media_folder_id)
    THEN RETURN 'inconsistent'; END IF;
    b:=rec.binding_id;
    IF native_binding IS NOT NULL AND native_binding<>b THEN RETURN 'inconsistent'; END IF;
    native_binding:=b; native_seen:=true;
   ELSE
    IF rec.binding_id IS NOT NULL THEN RETURN 'inconsistent'; END IF;
    local_seen:=true;
   END IF;
  END LOOP;
  FOR rec IN SELECT media_folder_id FROM media_item_libraries WHERE content_id=k
   UNION ALL SELECT media_folder_id FROM episode_libraries WHERE episode_id=k
  LOOP
   c:=bloem_native_folder_class(rec.media_folder_id);
   IF c='inconsistent' THEN RETURN c; END IF;
   IF c='native' THEN
    SELECT id INTO b FROM bloem_storage_bindings WHERE folder_id=rec.media_folder_id;
    IF b IS NULL OR NOT EXISTS(SELECT 1 FROM media_files f JOIN bloem_storage_file_refs r ON r.media_file_id=f.id
     WHERE f.content_id=k AND f.media_folder_id=rec.media_folder_id AND r.binding_id=b)
    THEN RETURN 'inconsistent'; END IF;
    IF native_binding IS NOT NULL AND native_binding<>b THEN RETURN 'inconsistent'; END IF;
    native_binding:=b; native_seen:=true;
   ELSE local_seen:=true;
   END IF;
  END LOOP;
 END LOOP;
 IF native_seen THEN
  IF local_seen OR roles<>1 OR NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=supplied AND type='ebook')
  OR EXISTS(SELECT 1 FROM media_extras WHERE content_id=supplied OR parent_id=supplied)
  OR EXISTS(SELECT 1 FROM media_item_roots WHERE content_id=supplied)
  OR EXISTS(SELECT 1 FROM media_item_groups WHERE content_id=supplied)
  OR EXISTS(SELECT 1 FROM user_dropped_series WHERE series_id=supplied)
  OR EXISTS(SELECT 1 FROM watch_provider_dropped_items WHERE series_id=supplied)
  THEN RETURN 'inconsistent'; END IF;
  RETURN 'native';
 END IF;
 RETURN 'local';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.bloem_native_lock_item_keys(supplied text[]) RETURNS void
    LANGUAGE plpgsql
    AS $$

DECLARE k text;
BEGIN
 IF current_setting('transaction_isolation') <> 'read committed' THEN
 RAISE EXCEPTION USING ERRCODE='BN003', MESSAGE='Native storage admission unavailable';
END IF;
 FOR k IN SELECT DISTINCT v FROM unnest(supplied) AS x(v) WHERE v IS NOT NULL AND v<>'' ORDER BY v LOOP
  IF NOT pg_try_advisory_xact_lock(hashtextextended('bloem:native-item:'||k,8500003)) THEN
   RAISE EXCEPTION USING ERRCODE='BN003', MESSAGE='Native storage admission unavailable';
  END IF;
 END LOOP;
 PERFORM content_id FROM media_items WHERE content_id=ANY(supplied) ORDER BY content_id FOR UPDATE NOWAIT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_admin_playback_history_identity BEFORE UPDATE OF media_item_id ON public.admin_playback_history FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_bloem_native_libraries_guard BEFORE INSERT OR DELETE OR UPDATE ON public.bloem_native_libraries FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_marker();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE CONSTRAINT TRIGGER bloem_native_bloem_native_publication_permits_complete AFTER INSERT ON public.bloem_native_publication_permits DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.bloem_native_check_publication_complete();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_bloem_native_publication_permits_guard BEFORE INSERT OR DELETE OR UPDATE ON public.bloem_native_publication_permits FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_permit();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_bloem_storage_bindings_guard BEFORE INSERT OR DELETE OR UPDATE ON public.bloem_storage_bindings FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_binding();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE CONSTRAINT TRIGGER bloem_native_bloem_storage_file_refs_complete AFTER INSERT OR UPDATE ON public.bloem_storage_file_refs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.bloem_native_check_association_complete();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_bloem_storage_file_refs_guard BEFORE INSERT OR DELETE OR UPDATE ON public.bloem_storage_file_refs FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_ref();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_downloads_identity BEFORE UPDATE OF content_id, episode_id ON public.downloads FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('content_id', 'episode_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_ebook_reader_progress_identity BEFORE UPDATE OF content_id ON public.ebook_reader_progress FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('content_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_episode_libraries_video BEFORE INSERT OR UPDATE ON public.episode_libraries FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_video('episode_id', 'media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_episodes_video BEFORE INSERT OR UPDATE ON public.episodes FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_video('content_id', 'series_id', 'season_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_library_collection_items_identity BEFORE UPDATE OF media_item_id ON public.library_collection_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_extras_book BEFORE INSERT OR UPDATE ON public.media_extras FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_extra();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE CONSTRAINT TRIGGER bloem_native_media_files_complete AFTER INSERT OR UPDATE ON public.media_files DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.bloem_native_check_association_complete();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_files_guard BEFORE INSERT OR DELETE OR UPDATE ON public.media_files FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_file();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_folder_paths_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_folder_paths FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_folders_changed BEFORE UPDATE ON public.media_folders FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_changed();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_folders_protected BEFORE DELETE OR UPDATE OF id, type, name, enabled, allow_empty_cleanup_once, poster_path, sort_order, metadata_language, chapter_thumbnails_enabled, intro_detection_enabled, collection_ungrouped_sort_order, auto_translate_metadata, trailer_kinds, owner_id, realtime_monitoring, trickplay_enabled ON public.media_folders FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_protected();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_group_locations_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_group_locations FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_group_overrides_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_group_overrides FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_item_groups_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_item_groups FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE CONSTRAINT TRIGGER bloem_native_media_item_libraries_complete AFTER INSERT OR UPDATE ON public.media_item_libraries DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.bloem_native_check_association_complete();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_item_libraries_guard BEFORE INSERT OR DELETE OR UPDATE ON public.media_item_libraries FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_member();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_item_provider_ids_identity BEFORE UPDATE OF content_id, item_type ON public.media_item_provider_ids FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('content_id', 'item_type');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_item_roots_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_item_roots FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE CONSTRAINT TRIGGER bloem_native_media_items_complete AFTER INSERT OR UPDATE ON public.media_items DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.bloem_native_check_association_complete();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_items_guard BEFORE INSERT OR DELETE OR UPDATE ON public.media_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_item();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_media_root_overrides_folder BEFORE INSERT OR DELETE OR UPDATE ON public.media_root_overrides FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_observed_media_locations_folder BEFORE INSERT OR DELETE OR UPDATE ON public.observed_media_locations FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_scanned_media_groups_folder BEFORE INSERT OR DELETE OR UPDATE ON public.scanned_media_groups FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_scanned_media_roots_folder BEFORE INSERT OR DELETE OR UPDATE ON public.scanned_media_roots FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_seasons_video BEFORE INSERT OR UPDATE ON public.seasons FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_video('content_id', 'series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_series_root_match_queue_folder BEFORE INSERT OR DELETE OR UPDATE ON public.series_root_match_queue FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_skipped_media_roots_folder BEFORE INSERT OR DELETE OR UPDATE ON public.skipped_media_roots FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_folder_child('media_folder_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_audio_preferences_identity BEFORE UPDATE OF series_id ON public.user_audio_preferences FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_downloads_identity BEFORE UPDATE OF media_item_id ON public.user_downloads FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_dropped_series_book BEFORE INSERT OR UPDATE ON public.user_dropped_series FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_book_drop();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_dropped_series_identity BEFORE UPDATE OF series_id ON public.user_dropped_series FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_favorites_identity BEFORE UPDATE OF media_item_id ON public.user_favorites FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_history_hidden_items_identity BEFORE UPDATE OF media_item_id ON public.user_history_hidden_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_home_item_dismissals_identity BEFORE UPDATE OF media_item_id, series_id ON public.user_home_item_dismissals FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id', 'series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_personal_collection_items_identity BEFORE UPDATE OF media_item_id ON public.user_personal_collection_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_ratings_identity BEFORE UPDATE OF media_item_id ON public.user_ratings FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_series_playback_preferences_identity BEFORE UPDATE OF series_id ON public.user_series_playback_preferences FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_subtitle_preferences_identity BEFORE UPDATE OF series_id ON public.user_subtitle_preferences FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_watch_history_identity BEFORE UPDATE OF media_item_id ON public.user_watch_history FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_watch_progress_identity BEFORE UPDATE OF media_item_id ON public.user_watch_progress FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_user_watchlist_identity BEFORE UPDATE OF media_item_id ON public.user_watchlist FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_watch_provider_dropped_items_book BEFORE INSERT OR UPDATE ON public.watch_provider_dropped_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_book_drop();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_watch_provider_dropped_items_identity BEFORE UPDATE OF series_id ON public.watch_provider_dropped_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('series_id');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER bloem_native_watch_provider_rating_items_identity BEFORE UPDATE OF media_item_id ON public.watch_provider_rating_items FOR EACH ROW EXECUTE FUNCTION public.bloem_native_guard_identity('media_item_id');
-- +goose StatementEnd
