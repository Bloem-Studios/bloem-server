-- +goose Up
-- Pastime managed tracking is removed. Its historical migrations stay
-- unchanged and executable; this drops everything they created: the triggers
-- on Silo-written tables (and on organizations and organization_memberships),
-- their functions, and the bloem_managed_*, bloem_companion_* and bloem_list_*
-- tables and sequences.
--
-- Data:
-- * watch_provider_connections and their child rows (sync runs, history
--   exports, list, rating and dropped items, scrobble sessions) are Silo's and
--   are not touched. The connections enrollment wrote stay as ordinary
--   watch-sync rows for the plugin installation that owns provider
--   plugin:<installation>:pastime; that installation stays disabled until a
--   version built on public Silo capabilities replaces it in place.
-- * Collections a list binding published keep their items and become manual
--   collections: the bloem_list_binding marker leaves source_config.
-- * Favorites, watchlists, ratings, progress and watch history are Silo's and
--   are not touched.
--
-- No CASCADE: a dependent nobody knows about aborts the whole migration.
-- Triggers go before the data changes, so none of them fires during the
-- collection updates.
--
-- Locks: DROP TRIGGER and DROP TABLE take ACCESS EXCLUSIVE locks on users and
-- the other formerly triggered Silo tables and hold each until commit. The
-- lock_timeout caps each lock wait at 5 s, not the whole step: behind a
-- longer-running transaction the step rolls back whole and the next start
-- retries it, but locks already taken stay held through every later wait.
-- Run it with no other database clients.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';

DROP TRIGGER IF EXISTS bloem_managed_users ON public.users;
DROP TRIGGER IF EXISTS bloem_managed_profiles ON public.user_profiles;
DROP TRIGGER IF EXISTS bloem_managed_memberships ON public.organization_memberships;
DROP TRIGGER IF EXISTS bloem_managed_organizations ON public.organizations;
DROP TRIGGER IF EXISTS bloem_managed_history_insert ON public.user_watch_history;
DROP TRIGGER IF EXISTS bloem_managed_history_update ON public.user_watch_history;
DROP TRIGGER IF EXISTS bloem_managed_scrobbles_insert ON public.watch_provider_scrobble_sessions;
DROP TRIGGER IF EXISTS bloem_managed_scrobbles_update ON public.watch_provider_scrobble_sessions;
DROP TRIGGER IF EXISTS bloem_managed_connection_guard ON public.watch_provider_connections;
DROP TRIGGER IF EXISTS bloem_managed_auth_identities ON public.plugin_auth_identities;
DROP TRIGGER IF EXISTS bloem_managed_auth_identities_update ON public.plugin_auth_identities;
DROP TRIGGER IF EXISTS bloem_managed_auth_installations ON public.plugin_installations;
DROP TRIGGER IF EXISTS bloem_managed_favorite_changed ON public.user_favorites;
DROP TRIGGER IF EXISTS bloem_managed_watchlist_changed ON public.user_watchlist;
DROP TRIGGER IF EXISTS bloem_managed_rating_changed ON public.user_ratings;
DROP TRIGGER IF EXISTS bloem_companion_ebook_changes ON public.ebook_reader_progress;
DROP TRIGGER IF EXISTS bloem_companion_audiobook_changes ON public.user_watch_progress;

DROP FUNCTION IF EXISTS public.bloem_managed_identity_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_organization_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_history_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_scrobble_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_connection_guard();
DROP FUNCTION IF EXISTS public.bloem_managed_auth_identity_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_auth_installation_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_personal_changed();
DROP FUNCTION IF EXISTS public.bloem_managed_record_personal(jsonb, text, boolean);
DROP FUNCTION IF EXISTS public.bloem_companion_record_book_change();
DROP FUNCTION IF EXISTS public.bloem_managed_refresh_identity(integer);
DROP FUNCTION IF EXISTS public.bloem_managed_refresh_account(integer);
DROP FUNCTION IF EXISTS public.bloem_managed_verified_identity(integer);
DROP FUNCTION IF EXISTS public.bloem_managed_display_name(text, text);

UPDATE public.user_personal_collections
SET collection_type = CASE WHEN native AND collection_type = 'synced' THEN 'manual' ELSE collection_type END,
    source_config = source_config - 'bloem_list_binding'
WHERE source_config->>'bloem_list_binding' IS NOT NULL;
UPDATE public.library_collections
SET source_config = source_config - 'bloem_list_binding'
WHERE source_config->>'bloem_list_binding' IS NOT NULL;

DROP TABLE IF EXISTS public.bloem_list_request_intents;
DROP TABLE IF EXISTS public.bloem_list_receipts;
DROP TABLE IF EXISTS public.bloem_list_pages;
DROP TABLE IF EXISTS public.bloem_list_jobs;
DROP TABLE IF EXISTS public.bloem_list_bindings;
DROP TABLE IF EXISTS public.bloem_companion_book_clock;
DROP TABLE IF EXISTS public.bloem_companion_owned_copies;
DROP TABLE IF EXISTS public.bloem_managed_library_receipts;
DROP TABLE IF EXISTS public.bloem_managed_recommendation_feedback;
DROP TABLE IF EXISTS public.bloem_managed_personal_states;
DROP TABLE IF EXISTS public.bloem_managed_calendar_snapshots;
DROP TABLE IF EXISTS public.bloem_managed_playback_sources;
DROP TABLE IF EXISTS public.bloem_managed_events;
DROP TABLE IF EXISTS public.bloem_managed_enrollments;
DROP TABLE IF EXISTS public.bloem_managed_page_cursors;
DROP TABLE IF EXISTS public.bloem_managed_snapshot_rows;
DROP TABLE IF EXISTS public.bloem_managed_snapshots;
DROP TABLE IF EXISTS public.bloem_managed_grants;
DROP TABLE IF EXISTS public.bloem_managed_changes;
DROP TABLE IF EXISTS public.bloem_managed_profiles;
DROP TABLE IF EXISTS public.bloem_managed_clock;

-- The first two are owned by columns of tables dropped above and are normally
-- gone already; the book revision sequence stands alone.
DROP SEQUENCE IF EXISTS public.bloem_managed_playback_revision;
DROP SEQUENCE IF EXISTS public.bloem_managed_calendar_snapshots_revision_seq;
DROP SEQUENCE IF EXISTS public.bloem_companion_book_revision;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'Pastime managed-tracking removal is forward-only; restore the pre-release database backup with the matching old binary';
END $$;
-- +goose StatementEnd
