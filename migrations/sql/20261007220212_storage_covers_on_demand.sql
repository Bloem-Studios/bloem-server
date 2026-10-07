-- +goose Up
-- Storage covers are served on demand through their plugin instead of being
-- copied into artwork storage. A book's poster names its source's cover
-- (artworkkey.StorageCoverPath); the revision below must match
-- artworkkey.StorageCoverRevision. Covers the host already stored are
-- replaced, and the artwork revision GC trigger queues their objects for
-- deletion. Provider or manually chosen artwork is kept.
UPDATE public.media_items i
SET poster_path = 'bloem-storage://storage-covers/' || c.content_id || '/original.'
        || left(encode(sha256(convert_to(octet_length(c.cover_entry_id)::text || ':' || c.cover_entry_id || c.cover_revision, 'UTF8')), 'hex'), 32)
        || '.img'
FROM (
    SELECT DISTINCT ON (f.content_id) f.content_id, r.cover_entry_id, r.cover_revision
    FROM public.bloem_storage_file_refs r
    JOIN public.media_files f ON f.id = r.media_file_id
    WHERE r.cover_entry_id IS NOT NULL AND position('/' IN f.content_id) = 0
    ORDER BY f.content_id, r.media_file_id
) c
WHERE i.content_id = c.content_id
  AND (i.poster_path IS NULL OR i.poster_path = '' OR i.poster_path LIKE 'local/ebooks/%');

-- Nothing claims or fetches covers any more.
DROP INDEX public.bloem_storage_file_refs_pending_cover_idx;
ALTER TABLE public.bloem_storage_file_refs
    DROP COLUMN cover_claimed_until,
    DROP COLUMN cover_fetched_revision;

-- +goose Down
-- Restores the backfill's columns, every cover pending. Posters keep naming
-- their storage covers.
ALTER TABLE public.bloem_storage_file_refs
    ADD COLUMN cover_fetched_revision text,
    ADD COLUMN cover_claimed_until timestamptz;
CREATE INDEX bloem_storage_file_refs_pending_cover_idx ON public.bloem_storage_file_refs (cover_claimed_until NULLS FIRST)
    WHERE cover_entry_id IS NOT NULL AND cover_revision IS DISTINCT FROM cover_fetched_revision;
