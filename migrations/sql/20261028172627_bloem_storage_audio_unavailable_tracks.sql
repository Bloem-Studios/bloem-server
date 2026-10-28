-- +goose Up
-- A native audiobook track whose bytes could not be read this scan (absent,
-- changed or timed out) rejects its book for the run. Its kept probe marks
-- the book for a later scan to list again.
ALTER TABLE public.bloem_storage_audio_staged_tracks
 DROP CONSTRAINT bloem_storage_audio_staged_tracks_status_check,
 ADD CONSTRAINT bloem_storage_audio_staged_tracks_status_check CHECK (status IN ('ok','invalid','unsupported','unavailable'));
ALTER TABLE public.bloem_storage_audio_probes
 DROP CONSTRAINT bloem_storage_audio_probes_status_check,
 ADD CONSTRAINT bloem_storage_audio_probes_status_check CHECK (status IN ('ok','invalid','unavailable'));
CREATE INDEX bloem_storage_audio_probes_unavailable_idx
 ON public.bloem_storage_audio_probes (location_id) WHERE status='unavailable';

-- +goose Down
DROP INDEX public.bloem_storage_audio_probes_unavailable_idx;
DELETE FROM public.bloem_storage_audio_probes WHERE status='unavailable';
UPDATE public.bloem_storage_audio_staged_tracks SET status='invalid' WHERE status='unavailable';
ALTER TABLE public.bloem_storage_audio_probes
 DROP CONSTRAINT bloem_storage_audio_probes_status_check,
 ADD CONSTRAINT bloem_storage_audio_probes_status_check CHECK (status IN ('ok','invalid'));
ALTER TABLE public.bloem_storage_audio_staged_tracks
 DROP CONSTRAINT bloem_storage_audio_staged_tracks_status_check,
 ADD CONSTRAINT bloem_storage_audio_staged_tracks_status_check CHECK (status IN ('ok','invalid','unsupported'));
