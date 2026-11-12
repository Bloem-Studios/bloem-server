-- +goose Up
-- Text subtitles listed beside a native episode file. Each one is attached to
-- the episode's media file, never published as a catalog file of its own; its
-- entry and revision pin the sidecar a later read must find. A row goes with
-- its file, its location, or (at scan completion) its listed entry.
CREATE TABLE public.bloem_storage_episode_sidecars (
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 media_file_id bigint NOT NULL REFERENCES public.media_files(id) ON DELETE CASCADE,
 revision text NOT NULL CHECK (octet_length(revision) BETWEEN 1 AND 4096),
 name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 4096),
 format text NOT NULL CHECK (format IN ('srt','vtt','ass','ssa')),
 language text NOT NULL DEFAULT '' CHECK (octet_length(language) <= 64),
 forced boolean NOT NULL DEFAULT false,
 hearing_impaired boolean NOT NULL DEFAULT false,
 PRIMARY KEY (location_id, entry_id)
);
CREATE INDEX bloem_storage_episode_sidecars_file_idx ON public.bloem_storage_episode_sidecars (media_file_id);

-- +goose Down
DROP TABLE public.bloem_storage_episode_sidecars;
