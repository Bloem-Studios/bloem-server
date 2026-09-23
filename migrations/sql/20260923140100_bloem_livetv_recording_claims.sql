-- +goose Up
-- DVR recordings are claimed with a compare-and-swap lease so only one API
-- replica records a row, a user cancel is never overwritten, and a recording
-- whose owner died is resumed (as an interrupted, partial recording) by any
-- surviving replica once the lease expires. See internal/livetv/recorder.go.
ALTER TABLE livetv_recordings
    ADD COLUMN claim_token text NOT NULL DEFAULT '',
    ADD COLUMN claim_node_id text NOT NULL DEFAULT '',
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN tuner_session_id text NOT NULL DEFAULT '',
    ADD COLUMN start_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN segments integer NOT NULL DEFAULT 0,
    ADD COLUMN interrupted boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE livetv_recordings
    DROP COLUMN IF EXISTS interrupted,
    DROP COLUMN IF EXISTS segments,
    DROP COLUMN IF EXISTS start_attempts,
    DROP COLUMN IF EXISTS tuner_session_id,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS claim_node_id,
    DROP COLUMN IF EXISTS claim_token;
