-- +goose Up
-- +goose StatementBegin
-- Push notifications for the Bloem admin apps (docs/architecture/bloem-admin-push.md).
--
-- Bloem-only. Silo's push_devices is profile-scoped and accepts one Apple
-- topic; an administrator's phone is an account-level endpoint, so it gets its
-- own rows and the Silo tables stay as upstream wrote them.

-- One row per admin-app installation that registered for push. The token is
-- stored encrypted (GCM, bound to the row id); the display token that lets the
-- phone read its own notification text is stored only as a hash.
CREATE TABLE public.bloem_admin_push_devices (
    id                 text PRIMARY KEY,
    user_id            integer NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    installation_id    text NOT NULL,
    platform           text NOT NULL,
    apns_environment   text,
    apns_topic         text,
    token_ciphertext   text NOT NULL,
    token_hash         text NOT NULL,
    server_device_id   text NOT NULL,
    display_token_hash text NOT NULL,
    -- Kind ids this device switched off. Empty means every kind is on.
    disabled_kinds     text[] NOT NULL DEFAULT '{}',
    login_session_id   text,
    enabled            boolean NOT NULL DEFAULT true,
    last_success_at    timestamptz,
    last_failure_at    timestamptz,
    last_failure_code  text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bloem_admin_push_devices_platform_check CHECK (platform IN ('apple', 'android')),
    CONSTRAINT bloem_admin_push_devices_apple_check CHECK (
        (platform = 'apple' AND apns_environment IN ('production', 'sandbox') AND apns_topic IS NOT NULL)
        OR (platform = 'android' AND apns_environment IS NULL AND apns_topic IS NULL)
    ),
    CONSTRAINT bloem_admin_push_devices_installation_key UNIQUE (user_id, installation_id, platform),
    CONSTRAINT bloem_admin_push_devices_server_device_key UNIQUE (server_device_id),
    CONSTRAINT bloem_admin_push_devices_display_token_key UNIQUE (display_token_hash)
);
CREATE INDEX bloem_admin_push_devices_user_idx ON public.bloem_admin_push_devices (user_id) WHERE enabled;
-- A token belongs to one device row. A re-install that reuses the token under
-- a new installation id replaces the old row rather than doubling pushes.
CREATE UNIQUE INDEX bloem_admin_push_devices_token_idx ON public.bloem_admin_push_devices (platform, token_hash);

-- One row per notification. The id is derived from the kind, the subject, the
-- recipient and a dedupe window, so every API replica that sees the same event
-- inserts the same row once.
CREATE TABLE public.bloem_admin_push_deliveries (
    id            text PRIMARY KEY,
    user_id       integer NOT NULL REFERENCES public.users (id) ON DELETE CASCADE,
    kind          text NOT NULL,
    collapse_key  text NOT NULL,
    subject_type  text NOT NULL DEFAULT '',
    subject_id    text NOT NULL DEFAULT '',
    title         text NOT NULL,
    body          text NOT NULL,
    lock_text     text NOT NULL,
    badge         integer,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL
);
CREATE INDEX bloem_admin_push_deliveries_user_idx ON public.bloem_admin_push_deliveries (user_id, created_at DESC);
CREATE INDEX bloem_admin_push_deliveries_expiry_idx ON public.bloem_admin_push_deliveries (expires_at);

-- One send per delivery and device, retried on the same schedule as Silo's
-- push attempts.
CREATE TABLE public.bloem_admin_push_attempts (
    id                text PRIMARY KEY,
    delivery_id       text NOT NULL REFERENCES public.bloem_admin_push_deliveries (id) ON DELETE CASCADE,
    device_id         text NOT NULL REFERENCES public.bloem_admin_push_devices (id) ON DELETE CASCADE,
    attempt_number    integer NOT NULL DEFAULT 0,
    outcome           text NOT NULL DEFAULT 'pending',
    next_retry_at     timestamptz,
    relay_request_id  text,
    upstream_status   integer,
    upstream_reason   text,
    failure_message   text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bloem_admin_push_attempts_outcome_check CHECK (outcome IN ('pending', 'retrying', 'delivered', 'failed')),
    CONSTRAINT bloem_admin_push_attempts_delivery_device_key UNIQUE (delivery_id, device_id)
);
CREATE INDEX bloem_admin_push_attempts_due_idx ON public.bloem_admin_push_attempts (next_retry_at)
    WHERE outcome IN ('pending', 'retrying');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.bloem_admin_push_attempts;
DROP TABLE IF EXISTS public.bloem_admin_push_deliveries;
DROP TABLE IF EXISTS public.bloem_admin_push_devices;
-- +goose StatementEnd
