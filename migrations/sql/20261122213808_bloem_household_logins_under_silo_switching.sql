-- +goose Up
-- Household logins and Silo switching coexist. Stock Silo, if it serves this
-- database, knows nothing of households, so the household's rules move into
-- the database, where they hold whichever server writes:
--
-- 1. A login follows its main account's enabled state. Suspending a main
--    account switches its enabled logins off (users.enabled, which every
--    Silo admission check reads, plus household_disabled, which remembers
--    the household did it) and ends their sign-ins in the tables stock Silo
--    checks. Restoring it switches back on only the logins the household
--    switched off. An administrator's write of a login's enabled state is the
--    login's own state from then on, and enabling a login under a suspended
--    main account is refused. Bloem's account update used to do this in Go;
--    stock Silo writes users.enabled directly and never ran it.
-- 2. While switching is on, deleting a main account deletes its logins first,
--    as Bloem does, instead of the foreign key refusing stock Silo's delete.
-- 3. bloem_household_silo_holds lists the logins `silo silo-switching
--    prepare` held so stock Silo, which does not enforce a household's shared
--    pool, cannot let a household play beyond it. Bloem releases them when it
--    serves again; restoring a main account does not bring a held login back.
-- 4. The guard that kept logins and switching apart is dropped. Its function,
--    bloem_household_login_switching_guard, stays unused.

CREATE TABLE bloem_household_silo_holds (
    user_id integer PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    held_at timestamptz NOT NULL DEFAULT now()
);

-- Ends every sign-in of the accounts, in the statements Silo's own
-- revokeSignInsInTransaction (internal/auth/password_reset.go) runs.
-- +goose StatementBegin
CREATE FUNCTION bloem_household_revoke_sign_ins(ids integer[]) RETURNS void LANGUAGE sql AS $$
    UPDATE auth_sessions SET revoked_at = now()
    WHERE (user_id = ANY(ids) OR impersonator_user_id = ANY(ids)) AND revoked_at IS NULL;
    UPDATE abs_sessions SET revoked_at = now() WHERE user_id = ANY(ids) AND revoked_at IS NULL;
    DELETE FROM jellycompat_sessions WHERE streamapp_user_id = ANY(ids);
    UPDATE device_login_requests SET status = 'denied', updated_at = now()
    WHERE approved_by_user_id = ANY(ids) AND status = 'approved';
$$;
-- +goose StatementEnd

-- A write of a login's enabled column. The household's own writes change
-- household_disabled with it and pass; any other is an administrator's.
-- +goose StatementBegin
CREATE FUNCTION bloem_household_login_enabled_written() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    main_enabled boolean;
BEGIN
    IF NEW.household_disabled IS DISTINCT FROM OLD.household_disabled THEN
        RETURN NEW;
    END IF;
    IF NEW.enabled THEN
        SELECT enabled INTO main_enabled FROM users WHERE id = NEW.household_main_user_id FOR SHARE;
        IF NOT main_enabled THEN
            RAISE EXCEPTION 'bloem_household_main_disabled' USING ERRCODE = 'P0001';
        END IF;
    END IF;
    NEW.household_disabled := false;
    DELETE FROM bloem_household_silo_holds WHERE user_id = NEW.id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER users_household_login_enabled_written
    BEFORE UPDATE OF enabled ON users
    FOR EACH ROW WHEN (NEW.household_main_user_id IS NOT NULL)
    EXECUTE FUNCTION bloem_household_login_enabled_written();

-- A main account's enabled state changed: its logins follow.
-- +goose StatementBegin
CREATE FUNCTION bloem_household_follow_main_enabled() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    switched_off integer[];
BEGIN
    IF NEW.enabled THEN
        UPDATE users SET enabled = true, household_disabled = false,
            access_policy_revision = access_policy_revision + 1, updated_at = now()
        WHERE household_main_user_id = NEW.id AND household_disabled
          AND NOT EXISTS (SELECT 1 FROM bloem_household_silo_holds h WHERE h.user_id = users.id);
    ELSE
        WITH off AS (
            UPDATE users SET enabled = false, household_disabled = true,
                access_policy_revision = access_policy_revision + 1, updated_at = now()
            WHERE household_main_user_id = NEW.id AND enabled
            RETURNING id)
        SELECT array_agg(id ORDER BY id) INTO switched_off FROM off;
        IF switched_off IS NOT NULL THEN
            PERFORM bloem_household_revoke_sign_ins(switched_off);
        END IF;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER users_household_follow_main_enabled
    AFTER UPDATE OF enabled ON users
    FOR EACH ROW WHEN (NEW.household_main_user_id IS NULL AND OLD.enabled IS DISTINCT FROM NEW.enabled)
    EXECUTE FUNCTION bloem_household_follow_main_enabled();

-- +goose StatementBegin
CREATE FUNCTION bloem_household_delete_with_main() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF public.bloem_silo_switching_enabled() THEN
        DELETE FROM users WHERE household_main_user_id = OLD.id;
    END IF;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER users_household_delete_with_main
    BEFORE DELETE ON users
    FOR EACH ROW WHEN (OLD.household_main_user_id IS NULL)
    EXECUTE FUNCTION bloem_household_delete_with_main();

DROP TRIGGER IF EXISTS users_household_login_switching_guard ON users;

-- +goose Down
CREATE TRIGGER users_household_login_switching_guard
    BEFORE INSERT OR UPDATE OF household_main_user_id ON users
    FOR EACH ROW WHEN (NEW.household_main_user_id IS NOT NULL)
    EXECUTE FUNCTION bloem_household_login_switching_guard();
DROP TRIGGER IF EXISTS users_household_delete_with_main ON users;
DROP FUNCTION IF EXISTS bloem_household_delete_with_main();
DROP TRIGGER IF EXISTS users_household_follow_main_enabled ON users;
DROP FUNCTION IF EXISTS bloem_household_follow_main_enabled();
DROP TRIGGER IF EXISTS users_household_login_enabled_written ON users;
DROP FUNCTION IF EXISTS bloem_household_login_enabled_written();
DROP FUNCTION IF EXISTS bloem_household_revoke_sign_ins(integer[]);
DROP TABLE IF EXISTS bloem_household_silo_holds;
