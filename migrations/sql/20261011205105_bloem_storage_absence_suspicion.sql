-- +goose Up
-- A provider's full listing is not a point-in-time snapshot: an entry updated
-- while the listing runs may be left to the provider's next listing. An entry a
-- completed run's full listing omits is therefore only suspected absent; a
-- later completed run that has not listed it either confirms the absence. Any
-- listing clears the suspicion.
ALTER TABLE public.bloem_storage_entries ADD COLUMN absence_suspected_at timestamptz;
CREATE INDEX bloem_storage_entries_absence_suspected_idx
 ON public.bloem_storage_entries(source_key) WHERE absence_suspected_at IS NOT NULL;

-- +goose Down
DROP INDEX public.bloem_storage_entries_absence_suspected_idx;
ALTER TABLE public.bloem_storage_entries DROP COLUMN absence_suspected_at;
