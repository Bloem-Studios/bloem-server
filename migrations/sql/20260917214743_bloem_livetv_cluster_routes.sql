-- +goose Up
ALTER TABLE livetv_sessions
    ADD COLUMN owner_node_id text NOT NULL DEFAULT '',
    ADD COLUMN owner_instance_id uuid;
CREATE INDEX bloem_livetv_playback_owner ON livetv_sessions (playback_session_id) WHERE status='active';
CREATE TABLE bloem_livetv_compat_streams (
    id uuid PRIMARY KEY,
    native_session_id text NOT NULL REFERENCES livetv_sessions(id) ON DELETE CASCADE,
    opener_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_livetv_compat_native ON bloem_livetv_compat_streams(native_session_id);

-- +goose Down
DROP TABLE bloem_livetv_compat_streams;
DROP INDEX bloem_livetv_playback_owner;
ALTER TABLE livetv_sessions DROP COLUMN owner_node_id, DROP COLUMN owner_instance_id;
