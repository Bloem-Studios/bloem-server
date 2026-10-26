-- +goose Up
-- Bloem's music catalog is removed. 20260823190558_music_catalog added the
-- artist, album and track tables and the scanner filled them for libraries of
-- type 'music' or 'songs'; nothing reads them any more. Albums were
-- media_items of type 'music_album' and tracks were media_files of base type
-- 'music', so those rows and the libraries that held them go too.
-- +goose StatementBegin
DROP TABLE public.music_tracks;
DROP TABLE public.music_albums;
DROP TABLE public.music_artists;
DELETE FROM public.media_files WHERE base_type = 'music';
DELETE FROM public.media_items WHERE type = 'music_album';
DELETE FROM public.media_folders WHERE lower(btrim(type)) IN ('music', 'songs');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'bloem_remove_music is forward-only; restore a backup taken before it';
END;
$$;
-- +goose StatementEnd
