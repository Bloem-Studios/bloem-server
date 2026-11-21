-- +goose Up
-- A household's shared pool: the concurrent streams and transcodes every
-- account of a household (a main account and its logins) draws on together.
-- A storefront sets it on the main account; 0 is unlimited, as an account's
-- own limits are. It applies only while the main account has a login.
CREATE TABLE bloem_household_pools (
    main_user_id integer PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_streams integer NOT NULL CHECK (max_streams >= 0),
    max_transcodes integer NOT NULL CHECK (max_transcodes >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The household a playback reservation counts against: its main account's
-- id, or 0 for an account in no pooled household.
ALTER TABLE playback_capacity_reservations
    ADD COLUMN household_id bigint NOT NULL DEFAULT 0 CHECK (household_id >= 0);

CREATE INDEX playback_capacity_reservations_household_lease_idx
    ON playback_capacity_reservations (household_id, lease_until)
    WHERE household_id <> 0;

-- +goose Down
DROP INDEX IF EXISTS playback_capacity_reservations_household_lease_idx;
ALTER TABLE playback_capacity_reservations DROP COLUMN IF EXISTS household_id;
DROP TABLE IF EXISTS bloem_household_pools;
