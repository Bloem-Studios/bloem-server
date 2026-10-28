-- +goose Up
-- A native audiobook track's probe, kept for later scans of the same
-- revision so an unchanged track is not opened and probed again.
CREATE TABLE public.bloem_storage_audio_probes (
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 revision text NOT NULL CHECK (octet_length(revision) BETWEEN 1 AND 4096),
 status text NOT NULL CHECK (status IN ('ok','invalid')),
 probe jsonb CHECK (probe IS NULL OR octet_length(probe::text) <= 1048576),
 probed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (location_id,entry_id),
 CHECK ((status='ok')=(probe IS NOT NULL))
);

-- +goose Down
DROP TABLE public.bloem_storage_audio_probes;
