-- +goose Up
-- A household login sees what its main account sees: it sits in the main
-- account's access group, with its own limits on top. A plan change moves the
-- main account to another group by writing users.access_group_id directly,
-- so the move is carried to the logins here, whichever path made it: each
-- login, and its profiles that sat on the main account's old group, move to
-- the new group, and the login's access policy revision moves as an
-- access_group_id write gives. An account without logins is unaffected.
-- +goose StatementBegin
CREATE FUNCTION bloem_household_logins_follow_group() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE user_profiles p SET access_group_id = NEW.access_group_id, updated_at = now()
    FROM users l
    WHERE l.household_main_user_id = NEW.id AND p.user_id = l.id
      AND p.access_group_id IS NOT DISTINCT FROM OLD.access_group_id;
    UPDATE users SET access_group_id = NEW.access_group_id,
        access_policy_revision = access_policy_revision + 1, updated_at = now()
    WHERE household_main_user_id = NEW.id AND access_group_id IS DISTINCT FROM NEW.access_group_id;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER users_household_logins_follow_group
    AFTER UPDATE OF access_group_id ON users
    FOR EACH ROW WHEN (NEW.access_group_id IS DISTINCT FROM OLD.access_group_id)
    EXECUTE FUNCTION bloem_household_logins_follow_group();

-- +goose Down
DROP TRIGGER IF EXISTS users_household_logins_follow_group ON users;
DROP FUNCTION IF EXISTS bloem_household_logins_follow_group();
