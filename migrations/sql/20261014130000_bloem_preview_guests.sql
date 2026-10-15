-- +goose Up
-- Guest preview sessions (plan 4b). The settings row is written only through
-- internal/preview, never the generic settings API, so turning the preview
-- off always revokes its sessions.
CREATE TABLE public.bloem_preview_settings (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    host text NOT NULL DEFAULT '',
    issuer text NOT NULL DEFAULT '',
    account_id integer REFERENCES public.users(id) ON DELETE SET NULL,
    ticket_public_keys text[] NOT NULL DEFAULT '{}'
        CHECK (cardinality(ticket_public_keys) <= 2),
    session_ttl_seconds integer NOT NULL DEFAULT 7200
        CHECK (session_ttl_seconds BETWEEN 300 AND 86400),
    idle_ttl_seconds integer NOT NULL DEFAULT 1200
        CHECK (idle_ttl_seconds BETWEEN 60 AND 7200),
    max_concurrent integer NOT NULL DEFAULT 200
        CHECK (max_concurrent BETWEEN 1 AND 10000),
    brand text NOT NULL DEFAULT '',
    cta_url text NOT NULL DEFAULT '',
    cta_label text NOT NULL DEFAULT '',
    storefront_url text NOT NULL DEFAULT '',
    launch_url text NOT NULL DEFAULT '',
    maturity_ceiling text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT bloem_preview_settings_enabled_complete CHECK (
        NOT enabled OR (host <> '' AND issuer <> '' AND account_id IS NOT NULL
            AND cardinality(ticket_public_keys) >= 1)
    )
);
INSERT INTO public.bloem_preview_settings DEFAULT VALUES;

-- One row per guest session; deleted with it. client_bucket is an HMAC under
-- a key that never leaves process memory, never an address.
CREATE TABLE public.bloem_preview_guest_sessions (
    session_id text PRIMARY KEY REFERENCES public.auth_sessions(id) ON DELETE CASCADE,
    idle_ttl_seconds integer NOT NULL CHECK (idle_ttl_seconds > 0),
    client_bucket text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX bloem_preview_guest_sessions_bucket_idx
    ON public.bloem_preview_guest_sessions (client_bucket);

-- Consumed ticket ids, kept until the ticket would have expired anyway.
CREATE TABLE public.bloem_preview_ticket_uses (
    jti text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX bloem_preview_ticket_uses_expires_idx
    ON public.bloem_preview_ticket_uses (expires_at);

ALTER TABLE public.auth_sessions DROP CONSTRAINT auth_sessions_auth_method_check;
ALTER TABLE public.auth_sessions ADD CONSTRAINT auth_sessions_auth_method_check
    CHECK (auth_method IN ('account', 'direct_profile', 'preview_guest'));
ALTER TABLE public.auth_sessions DROP CONSTRAINT auth_sessions_direct_profile_binding_check;
ALTER TABLE public.auth_sessions ADD CONSTRAINT auth_sessions_direct_profile_binding_check CHECK (
    (auth_method IN ('account', 'preview_guest') AND profile_id IS NULL AND profile_credential_revision IS NULL) OR
    (auth_method = 'direct_profile' AND profile_id IS NOT NULL AND profile_credential_revision IS NOT NULL
     AND NOT public.bloem_login_text_blank(device_id))
);

-- The shared preview account signs in only by ticket exchange: every other
-- session-minting path (password, OIDC, device approval, profile login,
-- impersonation, plugin launch, anything upstream adds) fails here.
-- +goose StatementBegin
CREATE FUNCTION public.bloem_preview_session_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    shared boolean;
BEGIN
    -- Settings assignment locks this same user row before revoking old
    -- credentials. Check shared status only after that lock has settled.
    PERFORM id FROM public.users WHERE id = NEW.user_id FOR SHARE;
    shared := EXISTS (SELECT 1 FROM public.bloem_preview_settings WHERE account_id = NEW.user_id);
    IF shared AND NEW.auth_method IS DISTINCT FROM 'preview_guest' THEN
        RAISE EXCEPTION 'the shared preview account signs in only through a preview ticket'
            USING ERRCODE = '42501';
    END IF;
    IF NOT shared AND NEW.auth_method = 'preview_guest' THEN
        RAISE EXCEPTION 'a preview guest session must belong to the shared preview account'
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bloem_preview_session_guard
    BEFORE INSERT OR UPDATE OF user_id, auth_method ON public.auth_sessions
    FOR EACH ROW EXECUTE FUNCTION public.bloem_preview_session_guard();

-- +goose StatementBegin
CREATE FUNCTION public.bloem_preview_api_key_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM id FROM public.users WHERE id = NEW.user_id FOR SHARE;
    IF EXISTS (SELECT 1 FROM public.bloem_preview_settings WHERE account_id = NEW.user_id) THEN
        RAISE EXCEPTION 'the shared preview account cannot hold API keys' USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER bloem_preview_api_key_guard
    BEFORE INSERT OR UPDATE OF user_id ON public.api_keys
    FOR EACH ROW EXECUTE FUNCTION public.bloem_preview_api_key_guard();

-- +goose Down
DROP TRIGGER IF EXISTS bloem_preview_api_key_guard ON public.api_keys;
DROP FUNCTION IF EXISTS public.bloem_preview_api_key_guard();
DROP TRIGGER IF EXISTS bloem_preview_session_guard ON public.auth_sessions;
DROP FUNCTION IF EXISTS public.bloem_preview_session_guard();
DELETE FROM public.auth_sessions WHERE auth_method = 'preview_guest';
ALTER TABLE public.auth_sessions DROP CONSTRAINT auth_sessions_direct_profile_binding_check;
ALTER TABLE public.auth_sessions ADD CONSTRAINT auth_sessions_direct_profile_binding_check CHECK (
    (auth_method = 'account' AND profile_id IS NULL AND profile_credential_revision IS NULL) OR
    (auth_method = 'direct_profile' AND profile_id IS NOT NULL AND profile_credential_revision IS NOT NULL
     AND NOT public.bloem_login_text_blank(device_id))
);
ALTER TABLE public.auth_sessions DROP CONSTRAINT auth_sessions_auth_method_check;
ALTER TABLE public.auth_sessions ADD CONSTRAINT auth_sessions_auth_method_check
    CHECK (auth_method IN ('account', 'direct_profile'));
DROP TABLE IF EXISTS public.bloem_preview_ticket_uses;
DROP TABLE IF EXISTS public.bloem_preview_guest_sessions;
DROP TABLE IF EXISTS public.bloem_preview_settings;
