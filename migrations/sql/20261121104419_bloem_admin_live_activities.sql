-- +goose Up
-- +goose StatementBegin
-- Live Activities for the Bloem admin apps (docs/architecture/bloem-admin-push.md).
--
-- Bloem-only, like the other bloem_admin_push tables. An admin app registers each
-- scan or backup Live Activity it runs, with the activity's own APNs push token on
-- iOS and no token on Android, and the server keeps the activity current: through
-- the relay's live_activity mode on iOS, and a private_alert whose delivery id marks
-- it as a live update on Android. The token is stored encrypted (GCM, bound to the
-- activity id) and the row is deleted when the activity ends, the device goes, or the
-- activity is past its lifetime.
CREATE TABLE public.bloem_admin_live_activities (
    -- A UUID the app makes for the activity.
    activity_id      uuid PRIMARY KEY,
    device_id        text NOT NULL REFERENCES public.bloem_admin_push_devices (id) ON DELETE CASCADE,
    kind             text NOT NULL,
    -- The library id for a scan; NULL for a backup.
    subject_id       text,
    platform         text NOT NULL,
    token_ciphertext text,
    environment      text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- created_at + 8 hours: iOS ends an activity then, and the server stops sending.
    expires_at       timestamptz NOT NULL,
    -- Set in the same statement that queues the end, so nothing is sent after it.
    ended_at         timestamptz,
    last_sent_at     timestamptz,
    -- The aps.timestamp of the last send. Each send takes a larger one, in the
    -- database, so replicas and retries never send out of order.
    last_timestamp   bigint NOT NULL DEFAULT 0,
    -- What the activity was last sent: unchanged state is never sent again, and a
    -- changed phase word goes at once.
    last_state_hash  text,
    last_phase       text,
    -- The newest state waiting for its send (newest wins), and whether it ends the
    -- activity.
    pending_state    jsonb,
    pending_event    text,
    -- The 30 second update cadence, and a relay rate-limit hold that every send waits out.
    next_send_at     timestamptz,
    hold_until       timestamptz,
    CONSTRAINT bloem_admin_live_activities_kind_check CHECK (kind IN ('scan', 'backup')),
    CONSTRAINT bloem_admin_live_activities_platform_check CHECK (platform IN ('ios', 'android')),
    CONSTRAINT bloem_admin_live_activities_token_check CHECK (
        (platform = 'ios' AND token_ciphertext IS NOT NULL AND environment IN ('production', 'sandbox'))
        OR (platform = 'android' AND token_ciphertext IS NULL AND environment IS NULL)
    ),
    CONSTRAINT bloem_admin_live_activities_subject_check CHECK (
        (kind = 'scan' AND subject_id IS NOT NULL) OR (kind = 'backup' AND subject_id IS NULL)
    ),
    CONSTRAINT bloem_admin_live_activities_event_check CHECK (pending_event IS NULL OR pending_event IN ('update', 'end'))
);
CREATE INDEX bloem_admin_live_activities_device_idx ON public.bloem_admin_live_activities (device_id, created_at);
CREATE INDEX bloem_admin_live_activities_subject_idx ON public.bloem_admin_live_activities (kind, subject_id) WHERE ended_at IS NULL;
CREATE INDEX bloem_admin_live_activities_pending_idx ON public.bloem_admin_live_activities (next_send_at) WHERE pending_state IS NOT NULL;
CREATE INDEX bloem_admin_live_activities_expiry_idx ON public.bloem_admin_live_activities (expires_at);

-- A device's push-to-start token (iOS 17.2+), stored encrypted, and the kinds it
-- opted in to. Empty kinds or no token means the server never starts an activity.
ALTER TABLE public.bloem_admin_push_devices
    ADD COLUMN live_start_token_ciphertext text,
    ADD COLUMN live_start_kinds            text[] NOT NULL DEFAULT '{}';

-- One row per job the server push-started on a device, so every replica starts it
-- there once. Deleted with the device, and by the pruner after the job's lifetime.
CREATE TABLE public.bloem_admin_live_starts (
    device_id  text NOT NULL REFERENCES public.bloem_admin_push_devices (id) ON DELETE CASCADE,
    job_key    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, job_key)
);
CREATE INDEX bloem_admin_live_starts_created_idx ON public.bloem_admin_live_starts (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.bloem_admin_live_starts;
ALTER TABLE public.bloem_admin_push_devices
    DROP COLUMN IF EXISTS live_start_kinds,
    DROP COLUMN IF EXISTS live_start_token_ciphertext;
DROP TABLE IF EXISTS public.bloem_admin_live_activities;
-- +goose StatementEnd
