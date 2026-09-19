-- +goose Up
-- Dynamic Bloem policies discover enabled media_folders.id values (already
-- bigint). An int4 policy array makes even ordinary tenant creation fail when
-- the catalog contains a wide library ID. Keep the entire policy chain and its
-- rollback projections at the parent's width; do not filter or truncate IDs.
--
-- These ALTERs rewrite the policy tables and rebuild their indexes. Quiesce
-- writers, allow copy space, and size SILO_MIGRATE_TIMEOUT for the account and
-- invitation tables as well as groups/templates. Recycle application pools
-- after the schema change. This does not widen unrelated scalar child IDs.
--
-- Lock users before inspecting its column name: finalization renames the
-- compatibility projection in one transaction. The lock keeps that choice
-- stable without changing authority phase, writer fences, policy or revisions.
-- +goose StatementBegin
LOCK TABLE public.users, public.access_groups,
    public.entitlement_template_revisions, public.entitlement_policy_cohort_revisions,
    public.invitations, public.organization_memberships,
    public.legacy_user_policy_rollback_snapshot, public.membership_policy_rollback_snapshot
    IN ACCESS EXCLUSIVE MODE;
DO $$
DECLARE
    target text;
    policy_column text;
    user_column text;
    policy_trigger record;
    restore_triggers text[];
    statement text;
BEGIN
    SELECT min(attname::text) INTO user_column FROM pg_attribute
    WHERE attrelid = 'public.users'::regclass AND NOT attisdropped
      AND attname IN ('library_ids', 'rollback_membership_library_ids')
    HAVING count(*) = 1;
    IF user_column IS NULL THEN
        RAISE EXCEPTION 'bloem_policy_library_ids_bigint: expected exactly one users library policy projection';
    END IF;
    FOREACH target IN ARRAY ARRAY[
        'users', 'access_groups', 'entitlement_template_revisions',
        'entitlement_policy_cohort_revisions', 'invitations',
        'organization_memberships', 'legacy_user_policy_rollback_snapshot',
        'membership_policy_rollback_snapshot'
    ] LOOP
        policy_column := CASE WHEN target = 'users' THEN user_column ELSE 'library_ids' END;
        IF NOT EXISTS (
            SELECT 1 FROM pg_attribute
            WHERE attrelid = format('public.%I', target)::regclass
              AND attname = policy_column AND NOT attisdropped
              AND atttypid IN ('integer[]'::regtype, 'bigint[]'::regtype)
        ) THEN
            RAISE EXCEPTION 'bloem_policy_library_ids_bigint: unexpected type for public.%.%', target, policy_column;
        END IF;
        -- PostgreSQL rejects ALTER TYPE while UPDATE OF / WHEN triggers
        -- depend on the column. Save only those exact dependencies, then
        -- restore their definitions (including deferral) and enabled modes.
        -- Preserve operator annotations too: DROP would otherwise lose them.
        -- ACCESS EXCLUSIVE plus Goose's transaction leaves no unfenced window.
        restore_triggers := '{}';
        FOR policy_trigger IN
            SELECT DISTINCT t.tgname, t.tgenabled, pg_get_triggerdef(t.oid) AS definition,
                obj_description(t.oid, 'pg_trigger') AS description
            FROM pg_trigger t JOIN pg_depend d ON d.objid = t.oid AND d.classid = 'pg_trigger'::regclass
            JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
            WHERE d.refclassid = 'pg_class'::regclass AND a.attrelid = format('public.%I', target)::regclass
              AND a.attname = policy_column AND NOT t.tgisinternal
              AND t.tgrelid = a.attrelid
        LOOP
            restore_triggers := array_append(restore_triggers, policy_trigger.definition);
            restore_triggers := array_append(restore_triggers, format('ALTER TABLE public.%I %s TRIGGER %I', target,
                CASE policy_trigger.tgenabled WHEN 'D' THEN 'DISABLE' WHEN 'A' THEN 'ENABLE ALWAYS'
                    WHEN 'R' THEN 'ENABLE REPLICA' ELSE 'ENABLE' END, policy_trigger.tgname));
            restore_triggers := array_append(restore_triggers, format('COMMENT ON TRIGGER %I ON public.%I IS %L',
                policy_trigger.tgname, target, policy_trigger.description));
            EXECUTE format('DROP TRIGGER %I ON public.%I', policy_trigger.tgname, target);
        END LOOP;
        EXECUTE format('ALTER TABLE public.%I ALTER COLUMN %I TYPE bigint[] USING %I::bigint[]',
            target, policy_column, policy_column);
        FOREACH statement IN ARRAY restore_triggers LOOP
            EXECUTE statement;
        END LOOP;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Image rollback does not require schema rollback. Explicit Down is lossless
-- only while every policy/projection still fits int4. Lock all writers before
-- inspecting ALL arrays (including snapshots), then narrow in one transaction.
-- Do not clamp, discard or silently remove a library from an access policy.
-- +goose StatementBegin
LOCK TABLE public.users, public.access_groups,
    public.entitlement_template_revisions, public.entitlement_policy_cohort_revisions,
    public.invitations, public.organization_memberships,
    public.legacy_user_policy_rollback_snapshot, public.membership_policy_rollback_snapshot
    IN ACCESS EXCLUSIVE MODE;
DO $$
DECLARE
    target text;
    policy_column text;
    user_column text;
    out_of_range boolean;
    policy_trigger record;
    restore_triggers text[];
    statement text;
BEGIN
    SELECT min(attname::text) INTO user_column FROM pg_attribute
    WHERE attrelid = 'public.users'::regclass AND NOT attisdropped
      AND attname IN ('library_ids', 'rollback_membership_library_ids')
    HAVING count(*) = 1;
    IF user_column IS NULL THEN
        RAISE EXCEPTION 'bloem_policy_library_ids_bigint down: expected exactly one users library policy projection';
    END IF;
    FOREACH target IN ARRAY ARRAY[
        'users', 'access_groups', 'entitlement_template_revisions',
        'entitlement_policy_cohort_revisions', 'invitations',
        'organization_memberships', 'legacy_user_policy_rollback_snapshot',
        'membership_policy_rollback_snapshot'
    ] LOOP
        policy_column := CASE WHEN target = 'users' THEN user_column ELSE 'library_ids' END;
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I CROSS JOIN LATERAL unnest(%I) AS ids(id) WHERE ids.id < -2147483648 OR ids.id > 2147483647)',
            target, policy_column) INTO out_of_range;
        IF out_of_range THEN
            RAISE EXCEPTION 'bloem_policy_library_ids_bigint down: public.%.% contains an ID outside integer range', target, policy_column;
        END IF;
    END LOOP;
    FOREACH target IN ARRAY ARRAY[
        'users', 'access_groups', 'entitlement_template_revisions',
        'entitlement_policy_cohort_revisions', 'invitations',
        'organization_memberships', 'legacy_user_policy_rollback_snapshot',
        'membership_policy_rollback_snapshot'
    ] LOOP
        policy_column := CASE WHEN target = 'users' THEN user_column ELSE 'library_ids' END;
        restore_triggers := '{}';
        FOR policy_trigger IN
            SELECT DISTINCT t.tgname, t.tgenabled, pg_get_triggerdef(t.oid) AS definition,
                obj_description(t.oid, 'pg_trigger') AS description
            FROM pg_trigger t JOIN pg_depend d ON d.objid = t.oid AND d.classid = 'pg_trigger'::regclass
            JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
            WHERE d.refclassid = 'pg_class'::regclass AND a.attrelid = format('public.%I', target)::regclass
              AND a.attname = policy_column AND NOT t.tgisinternal
              AND t.tgrelid = a.attrelid
        LOOP
            restore_triggers := array_append(restore_triggers, policy_trigger.definition);
            restore_triggers := array_append(restore_triggers, format('ALTER TABLE public.%I %s TRIGGER %I', target,
                CASE policy_trigger.tgenabled WHEN 'D' THEN 'DISABLE' WHEN 'A' THEN 'ENABLE ALWAYS'
                    WHEN 'R' THEN 'ENABLE REPLICA' ELSE 'ENABLE' END, policy_trigger.tgname));
            restore_triggers := array_append(restore_triggers, format('COMMENT ON TRIGGER %I ON public.%I IS %L',
                policy_trigger.tgname, target, policy_trigger.description));
            EXECUTE format('DROP TRIGGER %I ON public.%I', policy_trigger.tgname, target);
        END LOOP;
        EXECUTE format('ALTER TABLE public.%I ALTER COLUMN %I TYPE integer[] USING %I::integer[]',
            target, policy_column, policy_column);
        FOREACH statement IN ARRAY restore_triggers LOOP
            EXECUTE statement;
        END LOOP;
    END LOOP;
END
$$;
-- +goose StatementEnd
