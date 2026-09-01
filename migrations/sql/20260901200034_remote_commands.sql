-- +goose Up

-- S-5a admin remote control (docs/specs/admin-remote-control.md §F).
--
-- One row per command an admin or household member sent to a client. The
-- issuer columns carry ids only (no PII): issued_by is an account id for
-- admin commands and a profile id for household commands. No FK to accounts:
-- the audit row outlives the issuer.
CREATE TABLE public.remote_commands (
    id text PRIMARY KEY,
    scope text NOT NULL,
    target_session_id text NOT NULL DEFAULT '',
    target_device_id text NOT NULL DEFAULT '',
    target_user_id integer NOT NULL DEFAULT 0,
    target_profile_id text NOT NULL DEFAULT '',
    tenant_id text NOT NULL DEFAULT '',
    name text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    issued_by text NOT NULL,
    issuer_kind text NOT NULL,
    reason text NOT NULL DEFAULT '',
    state text NOT NULL,
    result jsonb,
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    acked_at timestamptz,
    finished_at timestamptz,
    expires_at timestamptz,
    CONSTRAINT remote_commands_scope_check CHECK (scope IN ('session', 'device')),
    CONSTRAINT remote_commands_issuer_kind_check CHECK (issuer_kind IN ('admin', 'household')),
    CONSTRAINT remote_commands_state_check
        CHECK (state IN ('queued', 'sent', 'accepted', 'rejected', 'rejected_unsupported', 'done', 'failed', 'expired'))
);

CREATE INDEX remote_commands_target_session_state_idx
    ON public.remote_commands (target_session_id, state);
CREATE INDEX remote_commands_target_device_state_idx
    ON public.remote_commands (target_device_id, state);
CREATE INDEX remote_commands_created_idx
    ON public.remote_commands (created_at DESC, id DESC);

-- Capability handshake (§A): the remote_control block a device advertised,
-- persisted per (user, profile, device). A device without a row is not
-- controllable. Kept beside the command audit rather than on user_devices so
-- the Silo-identical device registry paths stay untouched.
CREATE TABLE public.remote_device_capabilities (
    user_id integer NOT NULL,
    profile_id text NOT NULL,
    device_id text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    commands jsonb NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT remote_device_capabilities_pkey PRIMARY KEY (user_id, profile_id, device_id),
    CONSTRAINT remote_device_capabilities_user_fkey
        FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE IF EXISTS public.remote_device_capabilities;
DROP TABLE IF EXISTS public.remote_commands;
