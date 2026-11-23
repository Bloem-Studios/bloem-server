-- +goose Up
-- A household's login cap (Bloem household logins): the most logins a main
-- account may have, set by the storefront beside the household's limits. A
-- new login, made fresh or from a profile, is refused once the household has
-- that many; logins already there keep working when the cap is lowered below
-- them, and moving a login back to a profile is always allowed. No row is no
-- cap. The row goes with its main account, whichever server deletes it, so
-- stock Silo's account delete is unaffected while Silo switching is on.
CREATE TABLE bloem_household_login_caps (
    main_user_id integer PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    max_logins integer NOT NULL CHECK (max_logins >= 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS bloem_household_login_caps;
