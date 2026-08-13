-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.user_profiles
    ADD COLUMN login_email text,
    ADD COLUMN password_hash text,
    ADD COLUMN credential_revision bigint NOT NULL DEFAULT 1 CHECK (credential_revision > 0),
    ADD CONSTRAINT user_profiles_direct_credential_pair_check
        CHECK ((login_email IS NULL) = (password_hash IS NULL));

ALTER TABLE public.auth_sessions
    ADD COLUMN profile_id text,
    ADD COLUMN profile_credential_revision bigint,
    ADD COLUMN device_id text NOT NULL DEFAULT '',
    ADD COLUMN auth_method text NOT NULL DEFAULT 'account'
        CHECK (auth_method IN ('account', 'direct_profile')),
    ADD CONSTRAINT auth_sessions_direct_profile_binding_check CHECK (
        (auth_method = 'account' AND profile_id IS NULL AND profile_credential_revision IS NULL) OR
        (auth_method = 'direct_profile' AND profile_id IS NOT NULL AND profile_credential_revision IS NOT NULL)
    ),
    ADD CONSTRAINT auth_sessions_user_profile_fkey
        FOREIGN KEY (user_id, profile_id)
        REFERENCES public.user_profiles (user_id, id)
        ON DELETE CASCADE;

CREATE INDEX auth_sessions_direct_profile_idx
    ON public.auth_sessions (user_id, profile_id)
    WHERE auth_method = 'direct_profile' AND revoked_at IS NULL;

CREATE TABLE public.login_email_registry (
    normalized_email text PRIMARY KEY CHECK (normalized_email = lower(btrim(normalized_email))),
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
SELECT lower(btrim(email)), id
FROM public.users
WHERE email IS NOT NULL AND btrim(email) <> '';

CREATE FUNCTION public.vondel_sync_account_login_email_registry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND OLD.email IS DISTINCT FROM NEW.email) THEN
        DELETE FROM public.login_email_registry
        WHERE account_id = OLD.id;
    END IF;

    IF TG_OP <> 'DELETE' AND NEW.email IS NOT NULL AND btrim(NEW.email) <> '' THEN
        INSERT INTO public.login_email_registry (normalized_email, account_id)
        VALUES (lower(btrim(NEW.email)), NEW.id);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER users_login_email_registry_sync
AFTER INSERT OR UPDATE OF email OR DELETE ON public.users
FOR EACH ROW EXECUTE FUNCTION public.vondel_sync_account_login_email_registry();

CREATE FUNCTION public.vondel_sync_profile_login_email_registry()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' OR (TG_OP = 'UPDATE' AND (OLD.login_email, OLD.password_hash) IS DISTINCT FROM (NEW.login_email, NEW.password_hash)) THEN
        DELETE FROM public.login_email_registry
        WHERE profile_user_id = OLD.user_id AND profile_id = OLD.id;
    END IF;
    IF TG_OP <> 'DELETE' AND NEW.login_email IS NOT NULL THEN
        INSERT INTO public.login_email_registry (normalized_email, profile_user_id, profile_id)
        VALUES (lower(btrim(NEW.login_email)), NEW.user_id, NEW.id);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;

CREATE TRIGGER user_profiles_login_email_registry_sync
AFTER INSERT OR UPDATE OF login_email, password_hash OR DELETE ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.vondel_sync_profile_login_email_registry();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS users_login_email_registry_sync ON public.users;
DROP FUNCTION IF EXISTS public.vondel_sync_account_login_email_registry();
DROP TRIGGER IF EXISTS user_profiles_login_email_registry_sync ON public.user_profiles;
DROP FUNCTION IF EXISTS public.vondel_sync_profile_login_email_registry();
DROP TABLE IF EXISTS public.login_email_registry;

DROP INDEX IF EXISTS public.auth_sessions_direct_profile_idx;
ALTER TABLE public.auth_sessions
    DROP CONSTRAINT IF EXISTS auth_sessions_user_profile_fkey,
    DROP CONSTRAINT IF EXISTS auth_sessions_direct_profile_binding_check,
    DROP COLUMN IF EXISTS auth_method,
    DROP COLUMN IF EXISTS device_id,
    DROP COLUMN IF EXISTS profile_credential_revision,
    DROP COLUMN IF EXISTS profile_id;

ALTER TABLE public.user_profiles
    DROP CONSTRAINT IF EXISTS user_profiles_direct_credential_pair_check,
    DROP COLUMN IF EXISTS credential_revision,
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS login_email;
-- +goose StatementEnd
