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
-- The native onboarding guards cannot be restored without the onboarding
-- code that maintained their marker and permit rows.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'library storage locations: irreversible migration';
END $$;
-- +goose StatementEnd
