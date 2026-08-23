-- +goose Up
CREATE TABLE public.music_artists (
    id text PRIMARY KEY,
    name text NOT NULL CHECK (btrim(name) <> ''),
    sort_name text NOT NULL DEFAULT '',
    artwork_path text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.music_albums (
    content_id text PRIMARY KEY REFERENCES public.media_items(content_id) ON DELETE CASCADE,
    artist_id text NOT NULL REFERENCES public.music_artists(id) ON DELETE RESTRICT,
    year integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (year IS NULL OR year > 0)
);

CREATE TABLE public.music_tracks (
    id text PRIMARY KEY,
    album_id text NOT NULL REFERENCES public.music_albums(content_id) ON DELETE CASCADE,
    artist_id text NOT NULL REFERENCES public.music_artists(id) ON DELETE RESTRICT,
    media_file_id integer NOT NULL UNIQUE REFERENCES public.media_files(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (btrim(title) <> ''),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    disc_number integer NOT NULL CHECK (disc_number > 0),
    track_number integer NOT NULL CHECK (track_number > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (album_id, disc_number, track_number, id)
);

CREATE INDEX music_albums_artist_id_idx ON public.music_albums (artist_id, content_id);
CREATE INDEX music_tracks_album_order_idx ON public.music_tracks (album_id, disc_number, track_number, id);
CREATE INDEX music_tracks_artist_id_idx ON public.music_tracks (artist_id, id);

-- +goose Down
DROP TABLE IF EXISTS public.music_tracks;
DROP TABLE IF EXISTS public.music_albums;
DROP TABLE IF EXISTS public.music_artists;
