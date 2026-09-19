-- +goose Up
-- +goose StatementBegin
-- Credentials are encrypted with SECRET_KEY and bound to both the tuner ID and
-- its reviewed origin. They never belong in tuner/channel URLs or guide JSON.
CREATE TABLE bloem_livetv_xtream_credentials (
    tuner_id text PRIMARY KEY REFERENCES livetv_tuners(id) ON DELETE CASCADE,
    credentials text NOT NULL CHECK (credentials LIKE 'enc:v1:%')
);

-- Counts actual upstream MPEG-TS connections (including recordings and raw
-- proxy requests), not merely playback intents. Claims are serialized on the
-- tuner row; renewals are fenced by an unpredictable, per-connection lease ID.
CREATE TABLE bloem_livetv_xtream_leases (
    lease_id uuid PRIMARY KEY,
    tuner_id text NOT NULL REFERENCES livetv_tuners(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
CREATE INDEX bloem_livetv_xtream_leases_tuner_idx
    ON bloem_livetv_xtream_leases(tuner_id, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Image rollback retains this additive schema. An explicit Down must never
-- silently erase provider credentials or release active connection ownership.
LOCK TABLE livetv_tuners, livetv_guide_sources, bloem_livetv_xtream_credentials, bloem_livetv_xtream_leases IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM livetv_tuners WHERE type = 'xtream')
       OR EXISTS (SELECT 1 FROM livetv_guide_sources WHERE type = 'xtream')
       OR EXISTS (SELECT 1 FROM bloem_livetv_xtream_credentials)
       OR EXISTS (SELECT 1 FROM bloem_livetv_xtream_leases) THEN
        RAISE EXCEPTION 'remove Xtream providers and stop their streams before explicit schema rollback';
    END IF;
END $$;
DROP TABLE bloem_livetv_xtream_leases;
DROP TABLE bloem_livetv_xtream_credentials;
-- +goose StatementEnd
