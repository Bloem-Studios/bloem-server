-- +goose Up
-- What the last probe of a `.strm` URL saw: the remote ETag and Last-Modified
-- validators and the probe itself. A rescan that finds the same URL with the
-- same remote size and validators reuses the stored probe instead of reading
-- the media again. Rows without a probe are probed as before.
ALTER TABLE bloem_remote_sources
    ADD COLUMN IF NOT EXISTS etag TEXT NOT NULL DEFAULT '' CHECK (octet_length(etag) <= 1024),
    ADD COLUMN IF NOT EXISTS last_modified TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS probe JSONB;

-- +goose Down
ALTER TABLE bloem_remote_sources
    DROP COLUMN IF EXISTS probe,
    DROP COLUMN IF EXISTS last_modified,
    DROP COLUMN IF EXISTS etag;
