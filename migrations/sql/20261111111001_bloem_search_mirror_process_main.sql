-- +goose Up
-- Each process's lease records the identity marker of the main Meilisearch it
-- last saw, empty until it has read one. After a failover the settle step waits
-- only for live processes that still talk to another main, or that started
-- before the swap and have not reported a main yet, so a second process of the
-- new copy no longer holds the settle up.
ALTER TABLE bloem_search_mirror_processes
    ADD COLUMN IF NOT EXISTS main_target TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE bloem_search_mirror_processes
    DROP COLUMN IF EXISTS main_target;
