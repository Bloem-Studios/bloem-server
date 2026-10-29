-- +goose Up
-- Free play: a guest whose launch ticket carries a signed allowance may stream
-- video until that many seconds of delivery are used. The storefront decides
-- who gets an allowance; the server only honours and meters it.
ALTER TABLE public.bloem_preview_settings
    ADD COLUMN free_play_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN free_play_max_active integer NOT NULL DEFAULT 20
        CHECK (free_play_max_active BETWEEN 1 AND 1000),
    ADD COLUMN free_play_url text NOT NULL DEFAULT '',
    ADD COLUMN free_play_label text NOT NULL DEFAULT ''
        CHECK (length(free_play_label) <= 80),
    ADD COLUMN free_play_seconds integer NOT NULL DEFAULT 1800
        CHECK (free_play_seconds BETWEEN 60 AND 14400);

-- One row per storefront grant. The first exchange fixes the allowance and
-- the expiry; later tickets for the same grant never raise either.
CREATE TABLE public.bloem_preview_play_grants (
    grant_id text PRIMARY KEY CHECK (grant_id ~ '^[A-Za-z0-9_-]{22,64}$'),
    allowance_seconds integer NOT NULL CHECK (allowance_seconds BETWEEN 60 AND 14400),
    used_seconds double precision NOT NULL DEFAULT 0 CHECK (used_seconds >= 0),
    expires_at timestamptz NOT NULL,
    last_delivery_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_preview_play_grants_recent_delivery
    ON public.bloem_preview_play_grants (last_delivery_at);

ALTER TABLE public.bloem_preview_guest_sessions
    ADD COLUMN play_grant_id text
        REFERENCES public.bloem_preview_play_grants (grant_id) ON DELETE SET NULL;

-- The playback sessions a grant started. Media requests authorized by a
-- stream token name only the playback session, so delivery finds the grant
-- to charge here; a playback session without a row never plays for a guest.
CREATE TABLE public.bloem_preview_play_sessions (
    playback_session_id text PRIMARY KEY,
    grant_id text NOT NULL REFERENCES public.bloem_preview_play_grants (grant_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE public.bloem_preview_play_sessions;
ALTER TABLE public.bloem_preview_guest_sessions DROP COLUMN play_grant_id;
DROP TABLE public.bloem_preview_play_grants;
ALTER TABLE public.bloem_preview_settings
    DROP COLUMN free_play_seconds,
    DROP COLUMN free_play_label,
    DROP COLUMN free_play_url,
    DROP COLUMN free_play_max_active,
    DROP COLUMN free_play_enabled;
