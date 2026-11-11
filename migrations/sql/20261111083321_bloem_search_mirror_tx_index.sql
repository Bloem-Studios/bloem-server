-- +goose NO TRANSACTION

-- +goose Up
-- The search mirror pages the outbox in (tx_id, id) order. Build the index
-- without blocking writers to the outbox.
-- A failed concurrent build can leave an invalid index that makes IF NOT
-- EXISTS skip the retry. Remove only that unusable artifact first.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        JOIN pg_index i ON i.indexrelid = c.oid
        WHERE n.nspname = 'public'
          AND c.relname = 'idx_catalog_search_index_events_tx'
          AND NOT i.indisvalid
    ) THEN
        DROP INDEX public.idx_catalog_search_index_events_tx;
    END IF;
END;
$$;
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_catalog_search_index_events_tx
    ON public.catalog_search_index_events (tx_id, id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS public.idx_catalog_search_index_events_tx;
