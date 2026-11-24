-- +goose Up
-- +goose StatementBegin
-- Quiet hours for an admin app device (docs/architecture/bloem-stream-insights.md).
-- During the window, from quiet_start_minute to quiet_end_minute of the day in
-- quiet_time_zone (it may cross midnight), the server sends the device only
-- the kinds in quiet_allow_kinds; it does not hold the others for later. All
-- three window columns are set together or all NULL.
ALTER TABLE public.bloem_admin_push_devices
    ADD COLUMN quiet_start_minute smallint,
    ADD COLUMN quiet_end_minute smallint,
    ADD COLUMN quiet_time_zone text,
    ADD COLUMN quiet_allow_kinds text[] NOT NULL DEFAULT '{}',
    ADD CONSTRAINT bloem_admin_push_devices_quiet_hours_check CHECK (
        (quiet_start_minute IS NULL AND quiet_end_minute IS NULL AND quiet_time_zone IS NULL)
        OR (quiet_start_minute BETWEEN 0 AND 1439 AND quiet_end_minute BETWEEN 0 AND 1439
            AND quiet_start_minute <> quiet_end_minute AND quiet_time_zone <> ''));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.bloem_admin_push_devices
    DROP CONSTRAINT IF EXISTS bloem_admin_push_devices_quiet_hours_check,
    DROP COLUMN IF EXISTS quiet_allow_kinds,
    DROP COLUMN IF EXISTS quiet_time_zone,
    DROP COLUMN IF EXISTS quiet_end_minute,
    DROP COLUMN IF EXISTS quiet_start_minute;
-- +goose StatementEnd
