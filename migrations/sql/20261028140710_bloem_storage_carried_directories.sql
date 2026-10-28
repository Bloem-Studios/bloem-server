-- +goose Up
-- A carried directory is one an incremental parent listing did not report as
-- changed, in a source whose change feed covers its directories' contents. The
-- run keeps its retained entries without listing it.
ALTER TABLE public.bloem_storage_scan_directories
 DROP CONSTRAINT bloem_storage_scan_directories_listing_mode_check,
 ADD CONSTRAINT bloem_storage_scan_directories_listing_mode_check CHECK (listing_mode IN ('pending','full','incremental','carried'));

-- +goose Down
DELETE FROM public.bloem_storage_scan_directories WHERE listing_mode='carried';
ALTER TABLE public.bloem_storage_scan_directories
 DROP CONSTRAINT bloem_storage_scan_directories_listing_mode_check,
 ADD CONSTRAINT bloem_storage_scan_directories_listing_mode_check CHECK (listing_mode IN ('pending','full','incremental'));
