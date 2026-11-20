-- +goose Up
-- A household login is an account of its own that belongs to a main account
-- (a customer's account): it signs in with its own credentials and follows the
-- main account's enabled state. household_disabled marks a login that was
-- switched off because its main account was, so enabling the main account
-- switches back on only those. Both columns accept upstream Silo's writes: an
-- account written without them is an ordinary account.
ALTER TABLE users
    ADD COLUMN household_main_user_id integer NULL REFERENCES users(id) ON DELETE RESTRICT,
    ADD COLUMN household_disabled boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT users_household_main_not_self CHECK (household_main_user_id IS DISTINCT FROM id);

CREATE INDEX users_household_main_idx ON users(household_main_user_id) WHERE household_main_user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_household_main_idx;
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_household_main_not_self,
    DROP COLUMN IF EXISTS household_disabled,
    DROP COLUMN IF EXISTS household_main_user_id;
