-- +goose Up
-- Old running journals lack directory/alias evidence and cannot safely resume.
UPDATE public.bloem_storage_scan_runs SET state='failed',lease_epoch=lease_epoch+1,lease_until=clock_timestamp()
 WHERE state='running';
ALTER TABLE public.bloem_storage_scan_directories
 ADD COLUMN listing_mode text NOT NULL DEFAULT 'pending' CHECK (listing_mode IN ('pending','full','incremental')),
 ADD COLUMN changes_since text NOT NULL DEFAULT '' CHECK (octet_length(changes_since)<=4096),
 ADD CONSTRAINT bloem_storage_directory_mode_token_check CHECK (listing_mode='incremental' OR changes_since='');
ALTER TABLE public.bloem_storage_location_directories
 ADD COLUMN last_listing_mode text CHECK (last_listing_mode IN ('full','incremental'));
CREATE TABLE public.bloem_storage_scan_directory_entries (
 run_id uuid NOT NULL,
 directory_id text NOT NULL,
 entry_id text NOT NULL CHECK (octet_length(entry_id) BETWEEN 1 AND 1024),
 removed boolean NOT NULL DEFAULT false,
 PRIMARY KEY(run_id,directory_id,entry_id),
 FOREIGN KEY(run_id,directory_id) REFERENCES public.bloem_storage_scan_directories(run_id,directory_id) ON DELETE CASCADE
);
-- +goose Down
LOCK TABLE public.bloem_storage_scan_runs,public.bloem_storage_scan_directories,
 public.bloem_storage_location_directories,public.bloem_storage_scan_directory_entries IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.bloem_storage_scan_runs WHERE state='running') THEN
  RAISE EXCEPTION 'location discovery retained: cannot discard running directory evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE public.bloem_storage_scan_directory_entries;
ALTER TABLE public.bloem_storage_location_directories DROP COLUMN last_listing_mode;
ALTER TABLE public.bloem_storage_scan_directories DROP CONSTRAINT bloem_storage_directory_mode_token_check,
 DROP COLUMN listing_mode,DROP COLUMN changes_since;
