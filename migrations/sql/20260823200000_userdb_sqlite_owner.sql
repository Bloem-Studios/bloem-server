-- +goose Up
CREATE TABLE userdb_sqlite_owner (
    singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    node_id    TEXT NOT NULL CHECK (btrim(node_id) <> ''),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE userdb_sqlite_owner IS
    'Durable single-node ownership fence for local SQLite user state';

-- +goose Down
DROP TABLE userdb_sqlite_owner;
