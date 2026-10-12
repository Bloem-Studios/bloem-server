-- +goose Up
CREATE TABLE public.bloem_storage_audio_staged_tracks (
 run_id uuid NOT NULL,
 directory_id text NOT NULL,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 location_id uuid NOT NULL REFERENCES public.library_storage_locations(id) ON DELETE CASCADE,
 revision text NOT NULL CHECK (octet_length(revision) BETWEEN 1 AND 4096),
 status text NOT NULL CHECK (status IN ('ok','invalid','unsupported')),
 entry bytea NOT NULL CHECK (octet_length(entry) <= 1048576),
 probe jsonb CHECK (probe IS NULL OR octet_length(probe::text) <= 1048576),
 staged_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (run_id,directory_id,entry_id),
 FOREIGN KEY (run_id,directory_id) REFERENCES public.bloem_storage_scan_directories(run_id,directory_id) ON DELETE CASCADE,
 FOREIGN KEY (run_id,location_id) REFERENCES public.bloem_storage_scan_runs(id,location_id) DEFERRABLE INITIALLY DEFERRED,
 CHECK ((status='ok')=(probe IS NOT NULL))
);
CREATE INDEX bloem_storage_audio_staged_tracks_location ON public.bloem_storage_audio_staged_tracks(location_id,run_id);

-- +goose Down
DROP TABLE public.bloem_storage_audio_staged_tracks;
