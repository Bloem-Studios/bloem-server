-- +goose Up
CREATE SEQUENCE playback_capacity_reservation_generation_seq;

CREATE TABLE playback_capacity_reservations (
    session_id text PRIMARY KEY,
    generation bigint NOT NULL DEFAULT nextval('playback_capacity_reservation_generation_seq') CHECK (generation > 0),
    account_id bigint NOT NULL CHECK (account_id > 0),
    profile_id text NOT NULL,
    tenant_id text NOT NULL DEFAULT '',
    is_transcode boolean NOT NULL,
    lease_until timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER SEQUENCE playback_capacity_reservation_generation_seq
    OWNED BY playback_capacity_reservations.generation;

CREATE INDEX playback_capacity_reservations_account_lease_idx
    ON playback_capacity_reservations (account_id, lease_until);
CREATE INDEX playback_capacity_reservations_tenant_transcode_lease_idx
    ON playback_capacity_reservations (tenant_id, lease_until)
    WHERE is_transcode AND tenant_id <> '';

-- +goose Down
DROP TABLE playback_capacity_reservations;
