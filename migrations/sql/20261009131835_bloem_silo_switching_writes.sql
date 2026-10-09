-- +goose Up
-- +goose StatementBegin
-- While the database is mirrored for Silo switching, upstream Silo creates
-- accounts and profiles without an organization. Place them the way
-- pgstore.CreateProfile places a legacy profile: in the default organization,
-- under the account's membership there (created if missing, its policy seeded
-- from users by seed_legacy_membership_policy), with the membership's group or
-- the organization's default group. Outside 'mirrored' these are no-ops.
CREATE FUNCTION public.bloem_ensure_default_membership(target_account bigint)
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
    -- Unmarked on purpose: in 'mirrored' the seed trigger copies the account's
    -- policy from users, which is what Silo just wrote.
    INSERT INTO public.organization_memberships (organization_id, account_id, status, legacy_role)
    SELECT public.bloem_default_organization_id(), target_account, 'active',
           CASE WHEN account_role = 'admin' THEN 'admin' ELSE 'user' END
    ON CONFLICT (organization_id, account_id) DO NOTHING;
END;
$$;

CREATE FUNCTION public.bloem_silo_profile_tenancy()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    default_org uuid;
    membership_group bigint;
BEGIN
    IF NEW.organization_id IS NOT NULL
       OR NEW.user_id IS NULL
       OR (SELECT phase FROM public.membership_policy_authority WHERE singleton) <> 'mirrored' THEN
        RETURN NEW;
    END IF;
    default_org := public.bloem_default_organization_id();
    PERFORM public.bloem_ensure_default_membership(NEW.user_id);
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

-- Named to sort before user_profiles_entitlement_limit, which requires the
-- membership this creates.
CREATE TRIGGER user_profiles_00_silo_tenancy
BEFORE INSERT ON public.user_profiles
FOR EACH ROW EXECUTE FUNCTION public.bloem_silo_profile_tenancy();

CREATE FUNCTION public.bloem_silo_account_membership()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (SELECT phase FROM public.membership_policy_authority WHERE singleton) = 'mirrored'
       AND EXISTS (SELECT 1 FROM public.users WHERE id = NEW.id)
       AND NOT EXISTS (SELECT 1 FROM public.organization_memberships WHERE account_id = NEW.id) THEN
        PERFORM public.bloem_ensure_default_membership(NEW.id);
    END IF;
    RETURN NULL;
END;
$$;

-- Deferred to commit so Bloem's own account path, which inserts the account and
-- then its membership in one transaction, is never second-guessed. The trigger
-- exists only while the database is mirrored: a deferred trigger queues an
-- event on every account insert, and a queued event makes PostgreSQL refuse an
-- ALTER TABLE users later in the same transaction.
CREATE FUNCTION public.bloem_silo_switching_account_trigger(enabled boolean)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    DROP TRIGGER IF EXISTS users_silo_default_membership ON public.users;
    IF enabled THEN
        CREATE CONSTRAINT TRIGGER users_silo_default_membership
        AFTER INSERT ON public.users
        DEFERRABLE INITIALLY DEFERRED
        FOR EACH ROW EXECUTE FUNCTION public.bloem_silo_account_membership();
    END IF;
END;
$$;

CREATE FUNCTION public.bloem_silo_switching_phase_changed()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF (NEW.phase = 'mirrored') IS DISTINCT FROM (OLD.phase = 'mirrored') THEN
        PERFORM public.bloem_silo_switching_account_trigger(NEW.phase = 'mirrored');
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER membership_policy_authority_silo_switching
AFTER UPDATE OF phase ON public.membership_policy_authority
FOR EACH ROW EXECUTE FUNCTION public.bloem_silo_switching_phase_changed();

SELECT public.bloem_silo_switching_account_trigger(
    (SELECT phase = 'mirrored' FROM public.membership_policy_authority WHERE singleton)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER membership_policy_authority_silo_switching ON public.membership_policy_authority;
DROP FUNCTION public.bloem_silo_switching_phase_changed();
DROP TRIGGER IF EXISTS users_silo_default_membership ON public.users;
DROP FUNCTION public.bloem_silo_switching_account_trigger(boolean);
DROP FUNCTION public.bloem_silo_account_membership();
DROP TRIGGER user_profiles_00_silo_tenancy ON public.user_profiles;
DROP FUNCTION public.bloem_silo_profile_tenancy();
DROP FUNCTION public.bloem_ensure_default_membership(bigint);
-- +goose StatementEnd
