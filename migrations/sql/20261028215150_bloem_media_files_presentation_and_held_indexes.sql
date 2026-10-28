-- +goose NO TRANSACTION

-- +goose Up
-- The first-seen triggers on media_item_libraries look up a file's held
-- first-seen time by item and library on every insert. Few files hold one, so
-- a partial index answers that lookup however the trigger's plan is cached.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_files_held_first_seen
    ON public.media_files (content_id, media_folder_id)
    WHERE held_item_first_seen_at IS NOT NULL;

-- A native audiobook's publication reads and retires its library's files by
-- presentation group; without this each book scanned every file of the library.
-- It is partial so that lookups by library alone keep their own indexes; the
-- queries it serves state that the group is set. A full index of the same
-- columns, once built by hand, served those lookups too.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_media_files_folder_presentation_group;
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_media_files_presentation_group
    ON public.media_files (media_folder_id, presentation_group_key)
    WHERE presentation_group_key <> '';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.idx_media_files_presentation_group;
DROP INDEX CONCURRENTLY IF EXISTS public.idx_media_files_held_first_seen;
