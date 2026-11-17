-- +goose Up
-- +goose StatementBegin
-- Bloem drops profile sign-in: a household profile no longer has its own
-- login email and password, and no session is bound to one profile. This
-- removes what 20260813200000_profile_login_credentials added for it, and
-- the user_profiles branch of the Silo-switching guard. Account sign-in,
-- profile PINs and preview guest sessions are untouched; auth_sessions keeps
-- auth_method, which preview guests use.

-- Direct-profile sessions go first: the columns that bind them are dropped
-- below, and the auth_method check no longer admits them.
DELETE FROM public.auth_sessions WHERE auth_method = 'direct_profile';

DROP TRIGGER IF EXISTS user_profiles_mirrored_no_direct_login ON public.user_profiles;
DROP TRIGGER IF EXISTS user_profiles_revoke_rotated_sessions ON public.user_profiles;
DROP TRIGGER IF EXISTS user_profiles_rotate_credential_revision ON public.user_profiles;
DROP TRIGGER IF EXISTS user_profiles_login_email_registry_sync ON public.user_profiles;
DROP TRIGGER IF EXISTS users_login_email_registry_sync ON public.users;
DROP FUNCTION IF EXISTS public.bloem_revoke_rotated_profile_sessions();
DROP FUNCTION IF EXISTS public.bloem_rotate_profile_credential_revision();
DROP FUNCTION IF EXISTS public.bloem_sync_profile_login_email_registry();
DROP FUNCTION IF EXISTS public.bloem_sync_account_login_email_registry();
DROP TABLE IF EXISTS public.login_email_registry;

-- The guard keeps refusing what Silo cannot honor while switching is on: a
-- second organization and a membership outside the default organization.
CREATE OR REPLACE FUNCTION public.bloem_mirrored_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF NOT public.bloem_silo_switching_enabled() THEN
        RETURN NEW;
    END IF;
    -- Nested IFs: PL/pgSQL resolves every NEW field in an expression, so a
    -- table's columns may only be read inside that table's branch.
    IF TG_TABLE_NAME = 'organizations' THEN
        RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
    ELSIF TG_TABLE_NAME = 'organization_memberships' THEN
        IF NEW.organization_id IS DISTINCT FROM public.bloem_default_organization_id() THEN
            RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    RETURN NEW;
END;
$fn$;

DROP INDEX IF EXISTS public.auth_sessions_direct_profile_idx;
ALTER TABLE public.auth_sessions
    DROP CONSTRAINT IF EXISTS auth_sessions_direct_profile_binding_check,
    DROP CONSTRAINT IF EXISTS auth_sessions_user_profile_fkey,
    DROP COLUMN IF EXISTS profile_credential_revision,
    DROP COLUMN IF EXISTS profile_id,
    DROP CONSTRAINT IF EXISTS auth_sessions_auth_method_check,
    ADD CONSTRAINT auth_sessions_auth_method_check
        CHECK (auth_method IN ('account', 'preview_guest'));

ALTER TABLE public.user_profiles
    DROP CONSTRAINT IF EXISTS user_profiles_direct_credential_pair_check,
    DROP COLUMN IF EXISTS credential_revision,
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS login_email;

DROP FUNCTION IF EXISTS public.bloem_normalize_login_email(text);
DROP FUNCTION IF EXISTS public.bloem_login_text_blank(text);
DROP FUNCTION IF EXISTS public.bloem_login_whitespace();

DELETE FROM public.server_settings WHERE key = 'bloem.auth.profile_sign_in_enabled';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restores the schema shape profile sign-in had. No profile credential or
-- direct-profile session comes back; the login email registry is rebuilt
-- from account emails.
CREATE FUNCTION public.bloem_login_whitespace()
RETURNS text
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT U&' \0009\000A\000B\000C\000D\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'
$$;

CREATE FUNCTION public.bloem_login_text_blank(value text)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT value IS NULL OR btrim(value, public.bloem_login_whitespace()) = ''
$$;

CREATE FUNCTION public.bloem_normalize_login_email(value text)
RETURNS text
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT lower(btrim(value, public.bloem_login_whitespace()))
$$;

ALTER TABLE public.user_profiles
    ADD COLUMN login_email text,
    ADD COLUMN password_hash text,
    ADD COLUMN credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0),
    ADD CONSTRAINT user_profiles_direct_credential_pair_check
        CHECK (
            (login_email IS NULL AND password_hash IS NULL)
            OR (
                NOT public.bloem_login_text_blank(login_email)
                AND NOT public.bloem_login_text_blank(password_hash)
            )
        );

ALTER TABLE public.auth_sessions
    DROP CONSTRAINT IF EXISTS auth_sessions_auth_method_check,
    ADD CONSTRAINT auth_sessions_auth_method_check
        CHECK (auth_method IN ('account', 'direct_profile', 'preview_guest')),
    ADD COLUMN profile_id text,
    ADD COLUMN profile_credential_revision bigint,
    ADD CONSTRAINT auth_sessions_direct_profile_binding_check CHECK (
        (auth_method IN ('account', 'preview_guest') AND profile_id IS NULL AND profile_credential_revision IS NULL) OR
        (auth_method = 'direct_profile' AND profile_id IS NOT NULL AND profile_credential_revision IS NOT NULL
         AND NOT public.bloem_login_text_blank(device_id))
    ),
    ADD CONSTRAINT auth_sessions_user_profile_fkey
        FOREIGN KEY (user_id, profile_id)
        REFERENCES public.user_profiles (user_id, id)
        ON DELETE CASCADE;

CREATE INDEX auth_sessions_direct_profile_idx
    ON public.auth_sessions (user_id, profile_id)
    WHERE auth_method = 'direct_profile' AND revoked_at IS NULL;

CREATE TABLE public.login_email_registry (
    normalized_email text PRIMARY KEY
        CHECK (normalized_email = public.bloem_normalize_login_email(normalized_email)),
    account_id integer REFERENCES public.users(id) ON DELETE CASCADE,
    profile_user_id integer,
    profile_id text,
    CONSTRAINT login_email_registry_single_owner_check CHECK (
        ((account_id IS NOT NULL)::integer + (profile_id IS NOT NULL)::integer) = 1
        AND ((profile_user_id IS NOT NULL) = (profile_id IS NOT NULL))
    ),
    CONSTRAINT login_email_registry_profile_fkey
        FOREIGN KEY (profile_user_id, profile_id)
        REFERENCES public.user_profiles (user_id, id)
        ON DELETE CASCADE,
    CONSTRAINT login_email_registry_profile_owner_key UNIQUE (profile_user_id, profile_id)
);

INSERT INTO public.login_email_registry (normalized_email, account_id)
SELECT public.bloem_normalize_login_email(email), id
FROM public.users
WHERE NOT public.bloem_login_text_blank(email);

CREATE FUNCTION public.bloem_sync_account_login_email_registry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.email IS NOT DISTINCT FROM NEW.email THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        DELETE FROM public.login_email_registry
        WHERE account_id = OLD.id;
    END IF;
    IF TG_OP <> 'DELETE' AND NOT public.bloem_login_text_blank(NEW.email) THEN
        INSERT INTO public.login_email_registry (normalized_email, account_id)
        VALUES (public.bloem_normalize_login_email(NEW.email), NEW.id);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER users_login_email_registry_sync
AFTER INSERT OR UPDATE OF email OR DELETE ON public.users
FOR EACH ROW EXECUTE FUNCTION public.bloem_sync_account_login_email_registry();

CREATE FUNCTION public.bloem_sync_profile_login_email_registry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.login_email, OLD.password_hash) IS NOT DISTINCT FROM (NEW.login_email, NEW.password_hash) THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' OR TG_OP = 'UPDATE' THEN
        DELETE FROM public.login_email_registry
        WHERE profile_user_id = OLD.user_id AND profile_id = OLD.id;
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.login_email IS NOT NULL THEN
        INSERT INTO public.login_email_registry (normalized_email, profile_user_id, profile_id)
        VALUES (public.bloem_normalize_login_email(NEW.login_email), NEW.user_id, NEW.id);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER user_profiles_login_email_registry_sync
AFTER INSERT OR UPDATE OF login_email, password_hash OR DELETE ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_sync_profile_login_email_registry();

CREATE FUNCTION public.bloem_rotate_profile_credential_revision()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (OLD.login_email, OLD.password_hash) IS DISTINCT FROM (NEW.login_email, NEW.password_hash) THEN
        NEW.credential_revision := OLD.credential_revision + 1;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER user_profiles_rotate_credential_revision
BEFORE UPDATE OF login_email, password_hash ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_rotate_profile_credential_revision();

CREATE FUNCTION public.bloem_revoke_rotated_profile_sessions()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (OLD.login_email, OLD.password_hash) IS DISTINCT FROM (NEW.login_email, NEW.password_hash) THEN
        UPDATE public.auth_sessions
        SET revoked_at = now()
        WHERE user_id = NEW.user_id
          AND profile_id = NEW.id
          AND auth_method = 'direct_profile'
          AND revoked_at IS NULL;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER user_profiles_revoke_rotated_sessions
AFTER UPDATE OF login_email, password_hash ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_revoke_rotated_profile_sessions();

CREATE OR REPLACE FUNCTION public.bloem_mirrored_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF NOT public.bloem_silo_switching_enabled() THEN
        RETURN NEW;
    END IF;
    IF TG_TABLE_NAME = 'organizations' THEN
        RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
    ELSIF TG_TABLE_NAME = 'organization_memberships' THEN
        IF NEW.organization_id IS DISTINCT FROM public.bloem_default_organization_id() THEN
            RAISE EXCEPTION 'bloem_silo_switching_single_organization' USING ERRCODE = 'P0001';
        END IF;
    ELSIF TG_TABLE_NAME = 'user_profiles' THEN
        IF NEW.login_email IS NOT NULL OR NEW.password_hash IS NOT NULL THEN
            RAISE EXCEPTION 'bloem_silo_switching_direct_profile_login' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    RETURN NEW;
END;
$fn$;

CREATE TRIGGER user_profiles_mirrored_no_direct_login
BEFORE INSERT OR UPDATE OF login_email, password_hash ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_mirrored_guard();
-- +goose StatementEnd
