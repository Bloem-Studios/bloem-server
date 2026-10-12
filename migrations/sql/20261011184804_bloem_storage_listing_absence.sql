-- +goose Up
-- A directory listed without a change token lists every entry it holds, so a
-- completed run proves which of its former entries are gone. Record which
-- directory listings were full and which directory last listed each entry.
ALTER TABLE public.bloem_storage_scan_directories ADD COLUMN full_listing boolean NOT NULL DEFAULT false;
ALTER TABLE public.bloem_storage_entries ADD COLUMN directory_id text;

-- +goose Down
ALTER TABLE public.bloem_storage_entries DROP COLUMN directory_id;
ALTER TABLE public.bloem_storage_scan_directories DROP COLUMN full_listing;
