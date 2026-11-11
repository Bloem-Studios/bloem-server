-- +goose Up
-- The optional search mirror reads the search outbox with its own bookmark, a
-- Postgres transaction snapshot, so each event records the transaction that
-- wrote it. pg_current_xact_id() is the top-level id even inside a savepoint.
-- The column and its default are separate statements: ADD COLUMN with a
-- volatile default would rewrite the whole table. Rows written before this
-- migration keep a NULL tx_id: they predate the mirror, and its first full
-- copy covers them.
ALTER TABLE catalog_search_index_events
    ADD COLUMN IF NOT EXISTS tx_id xid8;

ALTER TABLE catalog_search_index_events
    ALTER COLUMN tx_id SET DEFAULT pg_current_xact_id();

-- The mirror's own state (bookmark, copy progress, identity) lives in a
-- column of its row in catalog_search_index_state, provider
-- 'meilisearch_mirror'. No such row exists unless a mirror is configured.
ALTER TABLE catalog_search_index_state
    ADD COLUMN IF NOT EXISTS bloem_mirror jsonb;

-- Items the mirror could not write after every retry and split.
CREATE TABLE IF NOT EXISTS bloem_search_mirror_dead_letters (
    content_id TEXT PRIMARY KEY,
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attempts INTEGER NOT NULL DEFAULT 1,
    last_error TEXT NOT NULL DEFAULT ''
);

-- One row per running Silo process that has the mirror configured; the
-- failover settle step reads the heartbeats to know when every process has
-- stopped writing to the old main index.
CREATE TABLE IF NOT EXISTS bloem_search_mirror_processes (
    instance_id UUID PRIMARY KEY,
    host TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heartbeat_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS bloem_search_mirror_processes;
DROP TABLE IF EXISTS bloem_search_mirror_dead_letters;

ALTER TABLE catalog_search_index_state
    DROP COLUMN IF EXISTS bloem_mirror;

ALTER TABLE catalog_search_index_events
    ALTER COLUMN tx_id DROP DEFAULT;

ALTER TABLE catalog_search_index_events
    DROP COLUMN IF EXISTS tx_id;
