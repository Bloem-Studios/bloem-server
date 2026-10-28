-- +goose Up
-- Choosing a run's next directory (pendingDirectoryTx) walks directories in
-- order; without this index every completed directory before the next pending
-- one is read again, so the cost grew with the run's progress.
CREATE INDEX bloem_storage_scan_directories_pending_idx
 ON public.bloem_storage_scan_directories (run_id, directory_id) WHERE NOT complete;

-- +goose Down
DROP INDEX public.bloem_storage_scan_directories_pending_idx;
