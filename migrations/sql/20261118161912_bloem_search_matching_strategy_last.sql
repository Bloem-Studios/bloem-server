-- +goose Up
-- Silo's keyword search now picks its own matching per search when the
-- strategy is "last": "all" for two words and "frequency" for three or more.
-- Its settings accept only "last" or "all", and a stored value they reject
-- makes the server fall back to Postgres search at startup. Bloem's earlier
-- "frequency" choice did what Silo's "last" now does, so store "last".
UPDATE public.server_settings
SET value = 'last'
WHERE key = 'catalog.search.meilisearch.matching_strategy'
  AND lower(btrim(value)) = 'frequency';

-- +goose Down
-- "last" now covers what "frequency" did; there is nothing to put back.
SELECT 1;
