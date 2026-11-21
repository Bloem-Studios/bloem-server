-- +goose Up
-- Upstream Silo knows nothing of household logins: after a switch to it, a
-- login would keep signing in while its main account is disabled. So, like a
-- profile with its own login, a household login and Silo switching never
-- coexist: enabling switching refuses while any login exists, and while
-- switching is on no account is made a login. The share lock on the switching
-- row orders a login being made against an enable in progress, which locks the
-- row before it counts the logins.
-- +goose StatementBegin
CREATE FUNCTION bloem_household_login_switching_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM 1 FROM public.bloem_silo_switching WHERE singleton FOR SHARE;
    IF public.bloem_silo_switching_enabled() THEN
        RAISE EXCEPTION 'bloem_silo_switching_household_login' USING ERRCODE = 'P0001';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER users_household_login_switching_guard
    BEFORE INSERT OR UPDATE OF household_main_user_id ON users
    FOR EACH ROW WHEN (NEW.household_main_user_id IS NOT NULL)
    EXECUTE FUNCTION bloem_household_login_switching_guard();

-- +goose Down
DROP TRIGGER IF EXISTS users_household_login_switching_guard ON users;
DROP FUNCTION IF EXISTS bloem_household_login_switching_guard();
