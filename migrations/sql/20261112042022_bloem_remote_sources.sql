-- +goose Up
-- The remote media behind a `.strm` catalog file, keyed by the file's catalog
-- path (media_files.file_path is unique), so the scanner can record it while
-- probing, before the file row has an id. The URL never appears in a path, a
-- response or a log; revision identifies it. source_revision is the storage
-- entry revision of a `.strm` object in a storage source, empty for a local
-- file. Rows are rebuilt by the next scan.
CREATE TABLE IF NOT EXISTS bloem_remote_sources (
    file_path TEXT PRIMARY KEY,
    url TEXT NOT NULL CHECK (url ~* '^https?://' AND octet_length(url) <= 8192),
    revision TEXT NOT NULL CHECK (revision <> ''),
    source_revision TEXT NOT NULL DEFAULT '',
    remote_size BIGINT NOT NULL CHECK (remote_size >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS bloem_remote_sources;
