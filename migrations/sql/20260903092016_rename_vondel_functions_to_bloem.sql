-- +goose Up
-- The 2026-08-26 rebrand (1a596b35) renamed these functions from vondel_* to
-- bloem_* by editing the migrations that create them instead of adding a
-- migration. A database created after that commit gets bloem_* names; one
-- migrated before it kept vondel_* and never learned the new ones. Production
-- is the second kind, so every Go call site naming a bloem_* function fails
-- there with SQLSTATE 42883: the invitations repository outright, and policy
-- decision logging as a dropped batch every few seconds.
--
-- Two steps are required, and the order matters.
--
-- 1. ALTER FUNCTION ... RENAME. Column defaults, triggers, indexes and
--    constraints store a parsed reference to the function OID rather than its
--    name, so they follow the rename with no further work.
--
-- 2. Rewrite the bodies. A function body is stored as TEXT and is NOT rewritten
--    by a rename, so a function that calls a sibling by name still names the
--    old one. Three do here:
--        vondel_login_text_blank        -> vondel_login_whitespace
--        vondel_normalize_login_email   -> vondel_login_whitespace
--        vondel_sync_*_login_email_registry -> the two above
--    Renaming without this step is what broke login on 2026-09-03: the trigger
--    on users fires on login, called normalize_login_email, and hit a
--    whitespace helper that no longer answered to that name.
--
-- Step 1 must come first. These are SQL-language functions, so CREATE OR
-- REPLACE parses the body immediately; rewriting a body to call bloem_* before
-- the rename has created that name would fail outright.
--
-- Argument lists come from oidvectortypes(proargtypes), which yields bare
-- types. pg_get_function_identity_arguments() is the obvious choice and is
-- wrong: it includes parameter names ("value text"), which ALTER FUNCTION
-- tolerates but to_regprocedure() rejects as an invalid type name.
--
-- Idempotent throughout, so it is a no-op on a database that already has
-- bloem_* and safe to re-run across a fleet in mixed states.
-- +goose StatementBegin
DO $$
DECLARE
    fn record;
    target text;
BEGIN
    -- Step 1: rename.
    FOR fn IN
        SELECT p.oid, p.proname, oidvectortypes(p.proargtypes) AS args
        FROM pg_proc p
        JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = 'public' AND p.proname LIKE 'vondel\_%'
    LOOP
        target := 'bloem_' || substring(fn.proname from 8);
        IF to_regprocedure(format('public.%I(%s)', target, fn.args)) IS NULL THEN
            EXECUTE format('ALTER FUNCTION public.%I(%s) RENAME TO %I', fn.proname, fn.args, target);
            RAISE NOTICE 'renamed public.% -> public.%', fn.proname, target;
        END IF;
    END LOOP;

    -- Step 2: repoint any body that still calls a sibling by its old name.
    FOR fn IN
        SELECT p.oid, p.proname
        FROM pg_proc p
        JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = 'public' AND p.prosrc ~ 'vondel_[a-z_]+'
    LOOP
        EXECUTE regexp_replace(pg_get_functiondef(fn.oid), 'vondel_([a-z_]+)', 'bloem_\1', 'g');
        RAISE NOTICE 'rewrote body of public.%', fn.proname;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE
    fn record;
    target text;
BEGIN
    FOR fn IN
        SELECT p.oid, p.proname, oidvectortypes(p.proargtypes) AS args
        FROM pg_proc p
        JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = 'public' AND p.proname LIKE 'bloem\_%'
    LOOP
        target := 'vondel_' || substring(fn.proname from 7);
        IF to_regprocedure(format('public.%I(%s)', target, fn.args)) IS NULL THEN
            EXECUTE format('ALTER FUNCTION public.%I(%s) RENAME TO %I', fn.proname, fn.args, target);
        END IF;
    END LOOP;

    FOR fn IN
        SELECT p.oid, p.proname
        FROM pg_proc p
        JOIN pg_namespace n ON n.oid = p.pronamespace
        WHERE n.nspname = 'public' AND p.prosrc ~ 'bloem_(login_whitespace|login_text_blank|normalize_login_email)'
    LOOP
        EXECUTE regexp_replace(pg_get_functiondef(fn.oid),
                               'bloem_(login_whitespace|login_text_blank|normalize_login_email)',
                               'vondel_\1', 'g');
    END LOOP;
END
$$;
-- +goose StatementEnd
