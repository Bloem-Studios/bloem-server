-- +goose Up
-- Converge Bloem's policy representation to current Silo without rewriting
-- either project's migration history. This also covers an upstream-first
-- installation where Silo has already recorded its newer migration versions.
-- Library access lists use integer[]; overflow aborts the entire transaction.
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
        IF (SELECT atttypid='integer[]'::regtype FROM pg_attribute WHERE attrelid=format('public.%I',target)::regclass AND attname=policy_column AND NOT attisdropped) THEN CONTINUE; END IF;
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
        IF (SELECT atttypid='integer[]'::regtype FROM pg_attribute WHERE attrelid=format('public.%I',target)::regclass AND attname=policy_column AND NOT attisdropped) THEN CONTINUE; END IF;
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

-- +goose StatementBegin
DO $$
DECLARE guard record; guards jsonb := '[]'; tbl text; account_column text;
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('bloem.membership_policy_handoff',0));
 FOR guard IN SELECT tgrelid::regclass::text AS tbl,tgname,tgenabled FROM pg_trigger
 WHERE NOT tgisinternal AND tgname IN ('users_membership_policy_authority_fence','organization_memberships_policy_writer_guard') LOOP
  guards := guards || jsonb_build_array(to_jsonb(guard));
  EXECUTE format('ALTER TABLE %s DISABLE TRIGGER %I',guard.tbl,guard.tgname);
 END LOOP;
 PERFORM set_config('bloem.membership_policy_writer',public.bloem_membership_policy_writer_marker(),true);
 account_column := CASE WHEN EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='users' AND column_name='max_profiles') THEN 'max_profiles' ELSE 'rollback_membership_max_profiles' END;
 EXECUTE format('ALTER TABLE users ALTER COLUMN %I DROP NOT NULL, ALTER COLUMN %I DROP DEFAULT',account_column,account_column);
 FOR tbl IN SELECT unnest(ARRAY['organization_memberships','legacy_user_policy_rollback_snapshot','membership_policy_rollback_snapshot']) LOOP
  EXECUTE format('ALTER TABLE %I ALTER COLUMN max_profiles DROP NOT NULL, ALTER COLUMN max_profiles DROP DEFAULT',tbl);
 END LOOP;
 EXECUTE format('UPDATE users SET %I=NULL WHERE %I=5',account_column,account_column);
 FOR tbl IN SELECT unnest(ARRAY['organization_memberships','legacy_user_policy_rollback_snapshot','membership_policy_rollback_snapshot']) LOOP
  EXECUTE format('UPDATE %I t SET max_profiles=NULL WHERE max_profiles=5',tbl);
  EXECUTE format('ALTER TABLE %I ALTER COLUMN max_profiles DROP NOT NULL, ALTER COLUMN max_profiles DROP DEFAULT',tbl);
 END LOOP;
 FOR guard IN SELECT * FROM jsonb_to_recordset(guards) AS x(tbl text,tgname text,tgenabled text) LOOP
  EXECUTE format('ALTER TABLE %s %s TRIGGER %I',guard.tbl,CASE guard.tgenabled WHEN 'D' THEN 'DISABLE' WHEN 'A' THEN 'ENABLE ALWAYS' WHEN 'R' THEN 'ENABLE REPLICA' ELSE 'ENABLE' END,guard.tgname);
 END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $adapter$
DECLARE definition text;
BEGIN
 definition:=pg_get_functiondef('public.enforce_user_profile_entitlement_limit()'::regprocedure);
 IF strpos(definition,'effective_limit := COALESCE(membership_limit, NULLIF(group_limit,0),5);')>0 THEN RETURN; END IF;
 IF strpos(definition,$old$effective_limit := CASE
        WHEN membership_limit > 0 AND group_limit > 0 THEN LEAST(membership_limit, group_limit)
        WHEN membership_limit > 0 THEN membership_limit
        WHEN group_limit > 0 THEN group_limit
        ELSE 0
    END;$old$)=0 THEN RAISE EXCEPTION 'Bloem profile-limit trigger changed'; END IF;
 EXECUTE replace(definition,$old$effective_limit := CASE
        WHEN membership_limit > 0 AND group_limit > 0 THEN LEAST(membership_limit, group_limit)
        WHEN membership_limit > 0 THEN membership_limit
        WHEN group_limit > 0 THEN group_limit
        ELSE 0
    END;$old$,$new$effective_limit := COALESCE(membership_limit, NULLIF(group_limit,0),5);
    IF group_managed THEN
        effective_limit := LEAST(effective_limit,GREATEST(group_limit,1));
    END IF;$new$);
END $adapter$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Removing references to nonexistent libraries cannot grant access. Permit
-- only that exact representation repair through the frozen policy guards.
DO $$
DECLARE name text; definition text; anchor text := 'PERFORM pg_advisory_xact_lock_shared(hashtextextended(''bloem.membership_policy_handoff'', 0));';
BEGIN
 FOREACH name IN ARRAY ARRAY['fence_legacy_user_policy_write','guard_membership_policy_write'] LOOP
  definition:=pg_get_functiondef(format('public.%I()',name)::regprocedure);
  IF strpos(definition,'public.existing_library_ids(OLD.library_ids)')>0 THEN CONTINUE; END IF;
  IF strpos(definition,anchor)=0 THEN RAISE EXCEPTION 'Bloem pruning: policy guard changed: %',name; END IF;
  EXECUTE replace(definition,anchor,anchor || $repair$
    IF TG_OP='UPDATE'
       AND (to_jsonb(NEW)-'library_ids'-'updated_at'-'admin_revision') IS NOT DISTINCT FROM (to_jsonb(OLD)-'library_ids'-'updated_at'-'admin_revision')
       AND NEW.library_ids IS DISTINCT FROM OLD.library_ids
       AND NEW.library_ids IS NOT DISTINCT FROM public.existing_library_ids(OLD.library_ids) THEN
        RETURN NEW;
    END IF;
$repair$);
 END LOOP;
END $$;

CREATE OR REPLACE FUNCTION public.bloem_prune_membership_library_ids(deleted integer[])
RETURNS void LANGUAGE plpgsql AS $$
DECLARE column_name text; target text; writer text:=current_setting('bloem.membership_policy_writer',true);
BEGIN
 PERFORM set_config('bloem.membership_policy_writer',public.bloem_membership_policy_writer_marker(),true);
 column_name:=CASE WHEN (SELECT phase FROM public.membership_policy_authority WHERE singleton)='finalized' THEN 'rollback_membership_library_ids' ELSE 'library_ids' END;
 EXECUTE format('UPDATE public.users SET %1$I=public.existing_library_ids(%1$I),updated_at=now() WHERE %1$I IS DISTINCT FROM public.existing_library_ids(%1$I) AND ($1 IS NULL OR %1$I && $1)',column_name) USING deleted;
 FOREACH target IN ARRAY ARRAY['organization_memberships','legacy_user_policy_rollback_snapshot','membership_policy_rollback_snapshot'] LOOP
  EXECUTE format('UPDATE public.%I SET library_ids=public.existing_library_ids(library_ids) WHERE library_ids IS DISTINCT FROM public.existing_library_ids(library_ids) AND ($1 IS NULL OR library_ids && $1)',target) USING deleted;
 END LOOP;
 PERFORM set_config('bloem.membership_policy_writer',COALESCE(writer,''),true);
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.prune_deleted_library_access_references()
RETURNS trigger
LANGUAGE plpgsql
AS $$
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
    PERFORM public.bloem_prune_membership_library_ids(deleted);
    UPDATE public.invitations
    SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
    WHERE library_ids && deleted;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.keep_existing_library_ids()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME='users' AND (SELECT phase FROM public.membership_policy_authority WHERE singleton)='finalized' THEN RETURN NEW; END IF;
    IF TG_OP = 'UPDATE' AND NEW.library_ids <@ OLD.library_ids THEN
        RETURN NEW;
    END IF;
    PERFORM 1 FROM public.media_folders WHERE id = ANY (NEW.library_ids) FOR KEY SHARE;
    NEW.library_ids := public.existing_library_ids(NEW.library_ids);
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.organization_memberships'::regclass AND tgname='organization_memberships_keep_existing_library_ids') THEN
  CREATE TRIGGER organization_memberships_keep_existing_library_ids BEFORE INSERT OR UPDATE OF library_ids ON public.organization_memberships FOR EACH ROW WHEN (NEW.library_ids IS NOT NULL) EXECUTE FUNCTION public.keep_existing_library_ids();
 END IF;
END $$;
-- +goose StatementEnd
SELECT public.bloem_prune_membership_library_ids(NULL);

-- +goose Down
-- Retain the standard representation and its data-preserving policy hooks.
-- Older migrations own explicit rollback of their schema boundaries.
SELECT 1;
