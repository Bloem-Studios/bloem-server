-- +goose Up
ALTER TABLE public.bloem_storage_location_edges ADD COLUMN absence_suspected_run uuid;

-- +goose Down
ALTER TABLE public.bloem_storage_location_edges DROP COLUMN absence_suspected_run;
