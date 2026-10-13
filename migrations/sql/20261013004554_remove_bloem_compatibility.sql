-- +goose Up
-- Compatibility-only data is removed after historical migrations finish.
-- Native canonical collections, progress and Live TV sessions are retained.
DROP TABLE IF EXISTS public.compat_application_credentials;
DROP TABLE IF EXISTS public.compat_application_audit;
DROP TABLE IF EXISTS public.compat_application_enrollments;
DROP TABLE IF EXISTS public.compat_applications;
DROP TABLE IF EXISTS public.bloem_livetv_compat_streams;
DROP TABLE IF EXISTS public.jellycompat_playback_sessions;
DROP TABLE IF EXISTS public.jellycompat_device_profiles;
DROP TABLE IF EXISTS public.jellycompat_displayprefs;
DROP TABLE IF EXISTS public.jellycompat_sessions;
DROP MATERIALIZED VIEW IF EXISTS public.abs_audiobook_author_counts;
DROP TABLE IF EXISTS public.abs_playlist_items;
DROP TABLE IF EXISTS public.abs_collection_items;
DROP TABLE IF EXISTS public.abs_smart_collections;
DROP TABLE IF EXISTS public.abs_playlists;
DROP TABLE IF EXISTS public.abs_user_collections;
DROP TABLE IF EXISTS public.abs_bookmarks;
DROP TABLE IF EXISTS public.abs_rss_feeds;
DROP TABLE IF EXISTS public.abs_playback_sessions;
DROP TABLE IF EXISTS public.abs_sessions;
DELETE FROM public.server_settings
WHERE starts_with(key, 'jellyfin_compat.')
   OR starts_with(key, 'audiobookshelf_compat.')
   OR starts_with(key, 'audiobooks.abs.');
DELETE FROM public.user_settings WHERE starts_with(key, 'jellycompat:');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'Compatibility removal is forward-only; restore the pre-release database backup with the matching old binary';
END $$;
-- +goose StatementEnd
