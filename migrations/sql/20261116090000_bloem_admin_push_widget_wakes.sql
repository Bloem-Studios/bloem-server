-- +goose Up
-- +goose StatementBegin
-- Widget wakes for the Bloem admin apps (docs/architecture/bloem-admin-push.md).
--
-- A wake is a silent push (the relay's background_wake) that tells an app with a
-- widget on its home screen to re-read what the widget shows. Only a device that
-- reported widgets_placed gets one, and only for the channels its widgets show.
-- The columns below hold what the app reported and the state the rate limit needs,
-- so every API replica applies the same limit to the same device.
ALTER TABLE public.bloem_admin_push_devices
    ADD COLUMN widgets_placed         boolean NOT NULL DEFAULT false,
    -- sessions, requests, libraries, nodes: what the placed widgets show.
    ADD COLUMN widget_channels        text[] NOT NULL DEFAULT '{}',
    -- Channels that changed since the last wake, and when the first of them did.
    -- Changes coalesce here for 30 seconds before one wake goes out.
    ADD COLUMN wake_pending_channels  text[] NOT NULL DEFAULT '{}',
    ADD COLUMN wake_pending_since     timestamptz,
    ADD COLUMN last_wake_at           timestamptz,
    -- The last time a visible alert push was queued for the device. A visible push
    -- already refreshes the widgets, so a wake never follows one within 2 minutes.
    ADD COLUMN last_visible_push_at   timestamptz;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.bloem_admin_push_devices
    DROP COLUMN IF EXISTS last_visible_push_at,
    DROP COLUMN IF EXISTS last_wake_at,
    DROP COLUMN IF EXISTS wake_pending_since,
    DROP COLUMN IF EXISTS wake_pending_channels,
    DROP COLUMN IF EXISTS widget_channels,
    DROP COLUMN IF EXISTS widgets_placed;
-- +goose StatementEnd
