-- +goose Up
-- The cover a storage provider offers for a file's book. Scans record it
-- without reading it; a background pass fetches covers whose revision has not
-- been fetched yet, claiming rows so several servers can share the work.
ALTER TABLE public.bloem_storage_file_refs
    ADD COLUMN cover_entry_id text CHECK (octet_length(cover_entry_id) BETWEEN 1 AND 1024),
    ADD COLUMN cover_revision text CHECK (octet_length(cover_revision) BETWEEN 1 AND 4096),
    ADD COLUMN cover_fetched_revision text,
    ADD COLUMN cover_claimed_until timestamptz,
    ADD CONSTRAINT bloem_storage_file_refs_cover_pair CHECK ((cover_entry_id IS NULL) = (cover_revision IS NULL));

-- Pending covers, oldest claim first.
CREATE INDEX bloem_storage_file_refs_pending_cover_idx ON public.bloem_storage_file_refs (cover_claimed_until NULLS FIRST)
    WHERE cover_entry_id IS NOT NULL AND cover_revision IS DISTINCT FROM cover_fetched_revision;

-- +goose Down
DROP INDEX public.bloem_storage_file_refs_pending_cover_idx;
ALTER TABLE public.bloem_storage_file_refs
    DROP CONSTRAINT bloem_storage_file_refs_cover_pair,
    DROP COLUMN cover_claimed_until,
    DROP COLUMN cover_fetched_revision,
    DROP COLUMN cover_revision,
    DROP COLUMN cover_entry_id;
