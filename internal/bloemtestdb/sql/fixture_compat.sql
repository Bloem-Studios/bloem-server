-- Bloem fixture compatibility for shared Go test databases.
--
-- NOT A MIGRATION. This file is installed only by cmd/bloem-testdb, only into
-- a database whose name marks it as a test database, and every trigger below
-- is a no-op unless the session carries bloem.fixture_compat = 'on' (set with
-- ALTER DATABASE by the same tool). Production databases never have these
-- objects, and the tenancy fences they sit beside are untouched: every row
-- written here satisfies the same constraints and triggers a production write
-- does.
--
-- Why: upstream Silo fixtures seed accounts, profiles, libraries and plugin
-- installations with bare INSERT/DELETE statements that predate Bloem's
-- organizations. Bloem requires a membership and an organization on every
-- profile and auto-entitles the default organization to every new media folder
-- and plugin installation (ON DELETE RESTRICT). These triggers perform the
-- tenancy bookkeeping the production write paths perform, so upstream test
-- files stay byte-identical to Silo's.
--
-- Idempotent: re-running replaces every object.

-- Memberships the shim created on a fixture's behalf. A fixture that later
-- writes its own membership for the account displaces them (see below).
CREATE TABLE IF NOT EXISTS public.bloem_fixture_shim_memberships (
    membership_id uuid PRIMARY KEY
);

-- Insert the default-organization membership a production account-creation
-- path would have written. Mirrors auth.insertDefaultMembershipPolicy: role
-- from users.role, the default organization's default group for non-admins,
-- the v1 policy-writer marker for the insert.
CREATE OR REPLACE FUNCTION public.bloem_fixture_ensure_default_membership(target_account bigint)
RETURNS void
LANGUAGE plpgsql
AS $$
DECLARE
    account_role text;
BEGIN
    SELECT role INTO account_role FROM public.users WHERE id = target_account;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    PERFORM set_config('bloem.membership_policy_writer', 'v1', true);
    PERFORM set_config('bloem.fixture_shim_writing', 'on', true);
    WITH created AS (
    INSERT INTO public.organization_memberships (organization_id, account_id, status, legacy_role, access_group_id)
    SELECT organizations.id,
           target_account,
           'active',
           CASE WHEN account_role = 'admin' THEN 'admin' ELSE 'user' END,
           CASE WHEN account_role = 'admin' THEN NULL ELSE (
               SELECT groups.id FROM public.access_groups AS groups
               WHERE groups.organization_id = organizations.id AND groups.is_default
           ) END
    FROM public.organizations
    WHERE organizations.is_default
    ON CONFLICT (organization_id, account_id) DO NOTHING
    RETURNING id
    )
    INSERT INTO public.bloem_fixture_shim_memberships (membership_id)
    SELECT id FROM created;
    PERFORM set_config('bloem.fixture_shim_writing', '', true);
END;
$$;

-- A fixture that inserts its own membership for an account (in any
-- organization) takes over from a shim-created one, exactly as if the shim
-- had never run: the shim membership is removed first, provided no profile
-- or policy decision refers to it (profiles would cascade away; decisions
-- restrict). A kept shim membership leaves the fixture's insert to behave as
-- it would have without the shim.
CREATE OR REPLACE FUNCTION public.bloem_fixture_displace_shim_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('bloem.fixture_compat', true) IS DISTINCT FROM 'on'
       OR current_setting('bloem.fixture_shim_writing', true) = 'on' THEN
        RETURN NEW;
    END IF;
    DELETE FROM public.organization_memberships AS memberships
    USING public.bloem_fixture_shim_memberships AS shim
    WHERE shim.membership_id = memberships.id
      AND memberships.account_id = NEW.account_id
      AND NOT EXISTS (
          SELECT 1 FROM public.user_profiles AS profiles
          WHERE profiles.organization_id = memberships.organization_id
            AND profiles.user_id = memberships.account_id
      )
      AND NOT EXISTS (
          SELECT 1 FROM public.policy_decisions AS decisions
          WHERE decisions.membership_id = memberships.id
      );
    DELETE FROM public.bloem_fixture_shim_memberships AS shim
    WHERE NOT EXISTS (SELECT 1 FROM public.organization_memberships WHERE id = shim.membership_id);
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS a_bloem_fixture_displace_shim_membership ON public.organization_memberships;
CREATE TRIGGER a_bloem_fixture_displace_shim_membership
BEFORE INSERT ON public.organization_memberships
FOR EACH ROW EXECUTE FUNCTION public.bloem_fixture_displace_shim_membership();

-- An account inserted without any membership by the time its transaction
-- commits gets the default-organization membership. Deferred to commit so a
-- production path that creates the account and then its (possibly tenant)
-- membership in the same transaction is never second-guessed: it already has a
-- membership when this runs.
CREATE OR REPLACE FUNCTION public.bloem_fixture_account_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('bloem.fixture_compat', true) IS DISTINCT FROM 'on' THEN
        RETURN NULL;
    END IF;
    IF EXISTS (SELECT 1 FROM public.organization_memberships WHERE account_id = NEW.id) THEN
        RETURN NULL;
    END IF;
    PERFORM public.bloem_fixture_ensure_default_membership(NEW.id);
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS a_bloem_fixture_account_membership ON public.users;
CREATE CONSTRAINT TRIGGER a_bloem_fixture_account_membership
AFTER INSERT ON public.users
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION public.bloem_fixture_account_membership();

-- A profile inserted without an organization is placed the way
-- pgstore.CreateProfile places a legacy profile: in the default organization,
-- under the account's membership there (created if missing), with the
-- membership's access group or the organization's default group. Named to sort
-- before user_profiles_entitlement_limit, which requires the membership.
CREATE OR REPLACE FUNCTION public.bloem_fixture_profile_org()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    default_org uuid;
    membership_group bigint;
BEGIN
    IF current_setting('bloem.fixture_compat', true) IS DISTINCT FROM 'on'
       OR NEW.organization_id IS NOT NULL
       OR NEW.user_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT id INTO default_org FROM public.organizations WHERE is_default;
    IF default_org IS NULL THEN
        RETURN NEW;
    END IF;
    PERFORM public.bloem_fixture_ensure_default_membership(NEW.user_id);
    SELECT access_group_id INTO membership_group
    FROM public.organization_memberships
    WHERE organization_id = default_org AND account_id = NEW.user_id;
    NEW.organization_id := default_org;
    IF NEW.access_group_id IS NULL THEN
        NEW.access_group_id := COALESCE(membership_group, (
            SELECT groups.id FROM public.access_groups AS groups
            WHERE groups.organization_id = default_org AND groups.is_default
        ));
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS a_bloem_fixture_profile_org ON public.user_profiles;
CREATE TRIGGER a_bloem_fixture_profile_org
BEFORE INSERT ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_fixture_profile_org();

-- A bare DELETE of a media folder or plugin installation first releases the
-- organization entitlements the default-organization trigger granted, which is
-- what catalog.FolderRepository.DeleteWithStats does before its own delete.
-- The ON DELETE RESTRICT foreign keys stay in place.
CREATE OR REPLACE FUNCTION public.bloem_fixture_release_media_folder_entitlements()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('bloem.fixture_compat', true) = 'on' THEN
        DELETE FROM public.organization_entitlements WHERE media_folder_id = OLD.id;
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS a_bloem_fixture_media_folder_entitlements ON public.media_folders;
CREATE TRIGGER a_bloem_fixture_media_folder_entitlements
BEFORE DELETE ON public.media_folders
FOR EACH ROW EXECUTE FUNCTION public.bloem_fixture_release_media_folder_entitlements();

CREATE OR REPLACE FUNCTION public.bloem_fixture_release_plugin_installation_entitlements()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF current_setting('bloem.fixture_compat', true) = 'on' THEN
        DELETE FROM public.organization_entitlements WHERE plugin_installation_id = OLD.id;
    END IF;
    RETURN OLD;
END;
$$;

DROP TRIGGER IF EXISTS a_bloem_fixture_plugin_installation_entitlements ON public.plugin_installations;
CREATE TRIGGER a_bloem_fixture_plugin_installation_entitlements
BEFORE DELETE ON public.plugin_installations
FOR EACH ROW EXECUTE FUNCTION public.bloem_fixture_release_plugin_installation_entitlements();
