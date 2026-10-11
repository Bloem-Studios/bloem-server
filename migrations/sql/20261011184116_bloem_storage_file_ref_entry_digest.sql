-- +goose Up
-- Digest of the listed entry a storage file was last published from. A
-- re-listing of the same entry skips republication; NULL republishes once.
ALTER TABLE public.bloem_storage_file_refs ADD COLUMN entry_sha256 bytea;

-- +goose Down
ALTER TABLE public.bloem_storage_file_refs DROP COLUMN entry_sha256;
