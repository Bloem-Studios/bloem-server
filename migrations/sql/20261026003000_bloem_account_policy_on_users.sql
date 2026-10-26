-- +goose Up
-- Account policy lives on users again, where Silo keeps it.
--
-- 20260829085838_membership_policy_isolation copied the 13 policy columns onto
-- organization_memberships and made them authoritative in three phases:
-- 'compatibility' froze every policy write, 'mirrored' kept users and the
-- default-organization membership identical for Silo switching, and
-- 'finalized' left users with a renamed rollback projection. An account belongs
-- to one organization, so its policy needs one home, and Silo already reads it
-- from users. Memberships become plain links: organization, account, status,
-- role and security revision, at most one per account.
--
-- Silo switching keeps working. Its guards (one organization, no profile logins)
-- and its placement of Silo-shaped writes now depend on bloem_silo_switching
-- instead of the policy phase: a database that was mirrored stays switchable.
-- +goose StatementBegin
CREATE TABLE public.bloem_silo_switching (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO public.bloem_silo_switching (enabled)
SELECT COALESCE((SELECT phase = 'mirrored' FROM public.membership_policy_authority WHERE singleton), false);

CREATE FUNCTION public.bloem_silo_switching_enabled()
RETURNS boolean
LANGUAGE sql
STABLE
AS $$
    SELECT COALESCE((SELECT enabled FROM public.bloem_silo_switching WHERE singleton), false)
$$;

-- The fence, mirror, seed and writer triggers all compare against the phase;
-- remove them before users is written below.
DROP TRIGGER IF EXISTS users_membership_policy_authority_fence ON public.users;
DROP TRIGGER IF EXISTS users_policy_mirror_to_membership ON public.users;
DROP TRIGGER IF EXISTS organization_memberships_policy_mirror_to_user ON public.organization_memberships;
DROP TRIGGER IF EXISTS organization_memberships_legacy_policy_seed ON public.organization_memberships;
DROP TRIGGER IF EXISTS organization_memberships_policy_writer_guard ON public.organization_memberships;
DROP TRIGGER IF EXISTS organization_memberships_keep_existing_library_ids ON public.organization_memberships;
DROP TRIGGER IF EXISTS membership_policy_authority_transition_guard ON public.membership_policy_authority;
DROP TRIGGER IF EXISTS membership_policy_authority_silo_switching ON public.membership_policy_authority;
DROP TRIGGER IF EXISTS membership_policy_rollout_observation_guard ON public.membership_policy_rollout_observations;
DROP TRIGGER IF EXISTS node_heartbeats_10_membership_policy_registration ON public.node_heartbeats;
DROP TRIGGER IF EXISTS node_heartbeats_membership_policy_delete_guard ON public.node_heartbeats;

-- A finalized database kept only a renamed projection on users; the policy is
-- on the memberships. Restore the names, then copy each account's policy back.
-- Mirrored and compatibility databases already hold it on users.
DO $$
DECLARE
    policy_column text;
BEGIN
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) IS DISTINCT FROM 'finalized' THEN
        RETURN;
    END IF;
    FOREACH policy_column IN ARRAY ARRAY[
        'access_group_id', 'permissions', 'library_ids', 'max_playback_quality',
        'max_streams', 'max_transcodes', 'transcode_allowed', 'audio_transcode_allowed',
        'download_allowed', 'download_transcode_allowed', 'requests_allowed',
        'max_profiles', 'access_policy_revision'
    ] LOOP
        IF EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'public' AND table_name = 'users'
              AND column_name = 'rollback_membership_' || policy_column
        ) THEN
            EXECUTE format('ALTER TABLE public.users RENAME COLUMN %I TO %I',
                'rollback_membership_' || policy_column, policy_column);
        END IF;
    END LOOP;
    UPDATE public.users AS accounts
    SET access_group_id = memberships.access_group_id,
        permissions = memberships.permissions,
        library_ids = memberships.library_ids,
        max_playback_quality = memberships.max_playback_quality,
        max_streams = memberships.max_streams,
        max_transcodes = memberships.max_transcodes,
        transcode_allowed = memberships.transcode_allowed,
        audio_transcode_allowed = memberships.audio_transcode_allowed,
        download_allowed = memberships.download_allowed,
        download_transcode_allowed = memberships.download_transcode_allowed,
        requests_allowed = memberships.requests_allowed,
        max_profiles = memberships.max_profiles,
        access_policy_revision = GREATEST(accounts.access_policy_revision, memberships.access_policy_revision)
    FROM public.organization_memberships AS memberships
    WHERE memberships.account_id = accounts.id;
END;
$$;

ALTER TABLE public.organization_memberships
    DROP COLUMN access_group_id,
    DROP COLUMN permissions,
    DROP COLUMN library_ids,
    DROP COLUMN max_playback_quality,
    DROP COLUMN max_streams,
    DROP COLUMN max_transcodes,
    DROP COLUMN transcode_allowed,
    DROP COLUMN audio_transcode_allowed,
    DROP COLUMN download_allowed,
    DROP COLUMN download_transcode_allowed,
    DROP COLUMN requests_allowed,
    DROP COLUMN max_profiles,
    DROP COLUMN access_policy_revision,
    ADD CONSTRAINT organization_memberships_one_per_account UNIQUE (account_id);

-- Heartbeats keep instance_id, which live TV routing matches; the capability
-- list only fed the rollout observations.
ALTER TABLE public.node_heartbeats
    DROP CONSTRAINT IF EXISTS node_heartbeats_membership_policy_observation_fkey,
    DROP COLUMN IF EXISTS membership_policy_rollout_observation_id,
    DROP COLUMN IF EXISTS schema_capabilities;
DROP TABLE public.membership_policy_rollout_observations;
DROP TABLE public.membership_policy_rollback_snapshot;
DROP TABLE IF EXISTS public.legacy_user_policy_rollback_snapshot;
DROP TABLE IF EXISTS public.profile_primary_rollback_snapshot;
DROP TABLE public.membership_policy_authority;

DROP FUNCTION public.bloem_membership_policy_writer_marker();
DROP FUNCTION public.fence_legacy_user_policy_write();
DROP FUNCTION public.seed_legacy_membership_policy();
DROP FUNCTION public.guard_membership_policy_write();
DROP FUNCTION public.guard_membership_policy_authority_transition();
DROP FUNCTION public.guard_membership_policy_rollout_observation();
DROP FUNCTION public.register_membership_policy_heartbeat();
DROP FUNCTION public.guard_node_heartbeat_delete();
DROP FUNCTION public.bloem_mirror_user_policy_to_membership();
DROP FUNCTION public.bloem_mirror_membership_policy_to_user();
DROP FUNCTION public.bloem_silo_switching_phase_changed();
DROP FUNCTION IF EXISTS public.bloem_prune_membership_library_ids(integer[]);

-- Silo's own library-reference pruning, as 20261013021242 defined it before
-- Bloem redirected it to the memberships.
CREATE OR REPLACE FUNCTION public.prune_deleted_library_access_references()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    deleted integer[];
BEGIN
    -- library_ids is integer[], so a library past its range can't be listed.
    SELECT array_agg(old_rows.id)::integer[] INTO deleted
    FROM old_rows WHERE old_rows.id <= 2147483647;
    IF deleted IS NULL THEN
        RETURN NULL; -- nothing listable deleted, or a retry found it gone
    END IF;
    UPDATE public.access_groups
    SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
    WHERE library_ids && deleted;
    UPDATE public.users
    SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
    WHERE library_ids && deleted;
    UPDATE public.invitations
    SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
    WHERE library_ids && deleted;
    RETURN NULL;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.keep_existing_library_ids()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.library_ids <@ OLD.library_ids THEN
        RETURN NEW;
    END IF;
    PERFORM 1 FROM public.media_folders WHERE id = ANY (NEW.library_ids) FOR KEY SHARE;
    NEW.library_ids := public.existing_library_ids(NEW.library_ids);
    RETURN NEW;
END;
$fn$;

DROP TRIGGER IF EXISTS users_keep_existing_library_ids ON public.users;
CREATE TRIGGER users_keep_existing_library_ids
    BEFORE INSERT OR UPDATE OF library_ids ON public.users
    FOR EACH ROW WHEN (NEW.library_ids IS NOT NULL)
    EXECUTE FUNCTION public.keep_existing_library_ids();
UPDATE public.users
SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
WHERE library_ids IS DISTINCT FROM public.existing_library_ids(library_ids);

-- The profile limit: the account's own override, else its group's.
CREATE OR REPLACE FUNCTION public.enforce_user_profile_entitlement_limit()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    account_limit integer;
    group_limit integer;
    group_managed boolean;
    effective_limit integer;
    existing_profiles integer;
BEGIN
    IF NEW.organization_id IS NULL OR NEW.user_id IS NULL THEN
        RETURN NEW;
    END IF;
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.organization_id::text || ':' || NEW.user_id::text, 0)
    );
    SELECT accounts.max_profiles,
           groups.max_profiles,
           groups.managed_template_key IS NOT NULL
      INTO account_limit, group_limit, group_managed
      FROM public.organization_memberships AS memberships
      JOIN public.users AS accounts ON accounts.id = memberships.account_id
      LEFT JOIN public.access_groups AS groups
        ON groups.id = NEW.access_group_id
       AND groups.organization_id = NEW.organization_id
     WHERE memberships.organization_id = NEW.organization_id
       AND memberships.account_id = NEW.user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'profile owner has no exact organization membership'
            USING ERRCODE = '23503', CONSTRAINT = 'user_profiles_organization_membership_fkey';
    END IF;
    IF group_managed AND group_limit = 0 THEN
        group_limit := 1;
    END IF;
    effective_limit := COALESCE(account_limit, NULLIF(group_limit, 0), 5);
    IF group_managed THEN
        effective_limit := LEAST(effective_limit, GREATEST(group_limit, 1));
    END IF;
    IF effective_limit > 0 THEN
        SELECT count(*)::integer INTO existing_profiles
          FROM public.user_profiles
         WHERE user_id = NEW.user_id
           AND organization_id = NEW.organization_id;
        IF existing_profiles >= effective_limit THEN
            RAISE EXCEPTION 'profile entitlement limit reached'
                USING ERRCODE = '23514', CONSTRAINT = 'user_profiles_entitlement_limit';
        END IF;
    END IF;
    RETURN NEW;
END;
$fn$;

-- An account's access group belongs to its organization, as the membership's
-- organization-scoped foreign key required while the policy lived there. The
-- account and its membership may be written in either order, so each side
-- checks against the other once both exist.
CREATE FUNCTION public.bloem_account_group_in_organization()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    account_group bigint;
    account_organization uuid;
BEGIN
    IF TG_TABLE_NAME = 'users' THEN
        account_group := NEW.access_group_id;
        SELECT organization_id INTO account_organization
        FROM public.organization_memberships WHERE account_id = NEW.id;
    ELSE
        account_organization := NEW.organization_id;
        SELECT access_group_id INTO account_group FROM public.users WHERE id = NEW.account_id;
    END IF;
    IF account_group IS NULL OR account_organization IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.access_groups
        WHERE id = account_group AND organization_id = account_organization
    ) THEN
        RAISE EXCEPTION 'account access group belongs to another organization'
            USING ERRCODE = '23503', CONSTRAINT = 'users_access_group_organization';
    END IF;
    RETURN NEW;
END;
$fn$;

CREATE TRIGGER users_access_group_organization
    BEFORE INSERT OR UPDATE OF access_group_id ON public.users
    FOR EACH ROW EXECUTE FUNCTION public.bloem_account_group_in_organization();
CREATE TRIGGER organization_memberships_account_group_organization
    BEFORE INSERT OR UPDATE OF organization_id, account_id ON public.organization_memberships
    FOR EACH ROW EXECUTE FUNCTION public.bloem_account_group_in_organization();

-- Silo switching: the same guards and write placement as before, keyed to the
-- switching flag rather than the policy phase.
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
    ELSIF TG_TABLE_NAME = 'user_profiles' THEN
        IF NEW.login_email IS NOT NULL OR NEW.password_hash IS NOT NULL THEN
            RAISE EXCEPTION 'bloem_silo_switching_direct_profile_login' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    RETURN NEW;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.bloem_mirror_user_role_to_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    previous_mirroring text := current_setting('bloem.membership_policy_mirroring', true);
    member_role text := CASE WHEN NEW.role = 'admin' THEN 'admin' ELSE 'user' END;
BEGIN
    IF previous_mirroring = 'on' OR NOT public.bloem_silo_switching_enabled() THEN
        RETURN NULL;
    END IF;
    PERFORM set_config('bloem.membership_policy_mirroring', 'on', true);
    UPDATE public.organization_memberships
    SET legacy_role = member_role, security_revision = security_revision + 1, updated_at = now()
    WHERE account_id = NEW.id
      AND organization_id = public.bloem_default_organization_id()
      AND legacy_role IS DISTINCT FROM member_role;
    PERFORM set_config('bloem.membership_policy_mirroring', COALESCE(previous_mirroring, ''), true);
    RETURN NULL;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.bloem_mirror_membership_role_to_user()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    previous_mirroring text := current_setting('bloem.membership_policy_mirroring', true);
BEGIN
    IF previous_mirroring = 'on'
       OR NOT public.bloem_silo_switching_enabled()
       OR NEW.organization_id IS DISTINCT FROM public.bloem_default_organization_id() THEN
        RETURN NULL;
    END IF;
    PERFORM set_config('bloem.membership_policy_mirroring', 'on', true);
    UPDATE public.users
    SET role = CASE WHEN NEW.legacy_role = 'admin' THEN 'admin' ELSE 'user' END, updated_at = now()
    WHERE id = NEW.account_id
      AND (role = 'admin') IS DISTINCT FROM (NEW.legacy_role = 'admin');
    PERFORM set_config('bloem.membership_policy_mirroring', COALESCE(previous_mirroring, ''), true);
    RETURN NULL;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.bloem_ensure_default_membership(target_account bigint)
RETURNS void
LANGUAGE plpgsql
AS $fn$
DECLARE
    account_role text;
BEGIN
    SELECT role INTO account_role FROM public.users WHERE id = target_account;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    INSERT INTO public.organization_memberships (organization_id, account_id, status, legacy_role)
    SELECT public.bloem_default_organization_id(), target_account, 'active',
           CASE WHEN account_role = 'admin' THEN 'admin' ELSE 'user' END
    ON CONFLICT (organization_id, account_id) DO NOTHING;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.bloem_silo_profile_tenancy()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
DECLARE
    default_org uuid;
    account_group bigint;
BEGIN
    IF NEW.organization_id IS NOT NULL
       OR NEW.user_id IS NULL
       OR NOT public.bloem_silo_switching_enabled() THEN
        RETURN NEW;
    END IF;
    default_org := public.bloem_default_organization_id();
    PERFORM public.bloem_ensure_default_membership(NEW.user_id);
    SELECT access_group_id INTO account_group FROM public.users WHERE id = NEW.user_id;
    NEW.organization_id := default_org;
    IF NEW.access_group_id IS NULL THEN
        NEW.access_group_id := COALESCE(account_group, (
            SELECT groups.id FROM public.access_groups AS groups
            WHERE groups.organization_id = default_org AND groups.is_default
        ));
    END IF;
    RETURN NEW;
END;
$fn$;

CREATE OR REPLACE FUNCTION public.bloem_silo_account_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF public.bloem_silo_switching_enabled()
       AND EXISTS (SELECT 1 FROM public.users WHERE id = NEW.id)
       AND NOT EXISTS (SELECT 1 FROM public.organization_memberships WHERE account_id = NEW.id) THEN
        PERFORM public.bloem_ensure_default_membership(NEW.id);
    END IF;
    RETURN NULL;
END;
$fn$;

-- Bloem applies a profile's own access group, so when Silo moves an account
-- the profiles that were on its old group move with it, as Bloem's own group
-- moves do. This is also what lets Silo delete a group after moving its members.
CREATE FUNCTION public.bloem_silo_profiles_follow_account_group()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF NEW.access_group_id IS NOT NULL
       AND NEW.access_group_id IS DISTINCT FROM OLD.access_group_id
       AND public.bloem_silo_switching_enabled() THEN
        UPDATE public.user_profiles
        SET access_group_id = NEW.access_group_id, updated_at = now()
        WHERE user_id = NEW.id
          AND organization_id = public.bloem_default_organization_id()
          AND access_group_id IS NOT DISTINCT FROM OLD.access_group_id;
    END IF;
    RETURN NULL;
END;
$fn$;

CREATE TRIGGER users_silo_profiles_follow_group
AFTER UPDATE OF access_group_id ON public.users
FOR EACH ROW EXECUTE FUNCTION public.bloem_silo_profiles_follow_account_group();

CREATE FUNCTION public.bloem_silo_switching_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $fn$
BEGIN
    IF NEW.enabled IS DISTINCT FROM OLD.enabled THEN
        PERFORM public.bloem_silo_switching_account_trigger(NEW.enabled);
    END IF;
    RETURN NULL;
END;
$fn$;

CREATE TRIGGER bloem_silo_switching_changed
AFTER UPDATE OF enabled ON public.bloem_silo_switching
FOR EACH ROW EXECUTE FUNCTION public.bloem_silo_switching_changed();

SELECT public.bloem_silo_switching_account_trigger(public.bloem_silo_switching_enabled());
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    RAISE EXCEPTION 'bloem_account_policy_on_users is forward-only; restore a backup taken before it';
END;
$$;
-- +goose StatementEnd
