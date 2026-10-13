-- +goose Up
-- Bloem's own compatibility persistence is removed after historical
-- migrations finish. Native canonical collections, progress and Live TV
-- sessions are retained.
--
-- The Jellyfin and Audiobookshelf tables, the author-count view and their
-- settings belong to upstream Silo, which can serve this database again while
-- the membership policy is mirrored (docs/architecture/silo-backend-switching.md).
-- Bloem no longer reads or writes them; they stay for Silo, including the
-- Jellyfin server identity its clients recognise.
DROP TABLE IF EXISTS public.compat_application_credentials;
DROP TABLE IF EXISTS public.compat_application_audit;
DROP TABLE IF EXISTS public.compat_application_enrollments;
DROP TABLE IF EXISTS public.compat_applications;
DROP TABLE IF EXISTS public.bloem_livetv_compat_streams;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'Compatibility removal is forward-only; restore the pre-release database backup with the matching old binary';
END $$;
-- +goose StatementEnd
