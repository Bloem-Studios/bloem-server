package migrations

import (
	"fmt"
	"io/fs"
	"strings"
)

func adaptBloemLibraryPruning(raw string) (string, error) {
	// Converge the downstream policy arrays back to upstream integer[].
	// Reuse the historical, lossless narrowing implementation with its
	// overflow checks and exact trigger restoration; leave history immutable.
	width, err := fs.ReadFile(FS, "sql/20260919095615_bloem_policy_library_ids_bigint.sql")
	if err != nil {
		return "", err
	}
	_, narrow, ok := strings.Cut(string(width), "-- +goose Down")
	if !ok {
		return "", fmt.Errorf("Bloem policy-width history changed")
	}
	raw = strings.Replace(raw, "-- +goose Up", "-- +goose Up\n"+narrow, 1)
	const userPrune = `    UPDATE public.users
    SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
    WHERE library_ids && deleted;`
	const userRepair = `UPDATE public.users
SET library_ids = public.existing_library_ids(library_ids), updated_at = now()
WHERE library_ids IS DISTINCT FROM public.existing_library_ids(library_ids);`
	if strings.Count(raw, userPrune) != 1 || strings.Count(raw, userRepair) != 1 {
		return "", fmt.Errorf("Bloem library-pruning adapter: upstream statements changed")
	}
	raw = strings.Replace(raw, userPrune, `    PERFORM public.bloem_prune_membership_library_ids(deleted);`, 1)
	raw = strings.Replace(raw, userRepair, `SELECT public.bloem_prune_membership_library_ids(NULL);`, 1)
	const userKeep = `CREATE TRIGGER users_keep_existing_library_ids
    BEFORE INSERT OR UPDATE OF library_ids ON public.users
    FOR EACH ROW WHEN (NEW.library_ids IS NOT NULL)
    EXECUTE FUNCTION public.keep_existing_library_ids();`
	if strings.Count(raw, userKeep) != 1 {
		return "", fmt.Errorf("Bloem pruning: account trigger changed")
	}
	raw = strings.Replace(raw, userKeep, `-- +goose StatementBegin
DO $$ BEGIN
 IF (SELECT phase FROM public.membership_policy_authority WHERE singleton)<>'finalized' THEN
`+userKeep+`
 END IF;
END $$;
-- +goose StatementEnd`, 1)
	const keep = `    IF TG_OP = 'UPDATE' AND NEW.library_ids <@ OLD.library_ids THEN`
	raw = strings.Replace(raw, keep, `    -- Finalization renames the unused account projection. The tenant
    -- membership remains authoritative and has its own pruning trigger.
    IF TG_TABLE_NAME='users' AND (SELECT phase FROM public.membership_policy_authority WHERE singleton)='finalized' THEN
        RETURN NEW;
    END IF;
`+keep, 1)
	pos := strings.Index(raw, "-- +goose StatementEnd")
	if pos < 0 {
		return "", fmt.Errorf("Bloem library-pruning adapter: missing function boundary")
	}
	pos += len("-- +goose StatementEnd")
	raw = raw[:pos] + "\n" + bloemLibraryPruningHelpers + "\n" + raw[pos:]
	raw = strings.Replace(raw, "-- Repair the references earlier deletes left behind.", `CREATE TRIGGER organization_memberships_keep_existing_library_ids
    BEFORE INSERT OR UPDATE OF library_ids ON public.organization_memberships
    FOR EACH ROW WHEN (NEW.library_ids IS NOT NULL)
    EXECUTE FUNCTION public.keep_existing_library_ids();

-- Repair the references earlier deletes left behind.`, 1)
	raw = strings.Replace(raw, "-- +goose Down", "-- +goose Down\n"+bloemLibraryPruningDown, 1)
	return raw, nil
}

const bloemLibraryPruningHelpers = `-- +goose StatementBegin
-- Removing references to nonexistent libraries cannot grant access. Permit
-- only that exact representation repair through the frozen policy guards.
DO $$
DECLARE name text; definition text; anchor text := 'PERFORM pg_advisory_xact_lock_shared(hashtextextended(''bloem.membership_policy_handoff'', 0));';
BEGIN
 FOREACH name IN ARRAY ARRAY['fence_legacy_user_policy_write','guard_membership_policy_write'] LOOP
  definition:=pg_get_functiondef(format('public.%I()',name)::regprocedure);
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

CREATE FUNCTION public.bloem_prune_membership_library_ids(deleted integer[])
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
-- +goose StatementEnd`

const bloemLibraryPruningDown = `-- +goose StatementBegin
DROP TRIGGER organization_memberships_keep_existing_library_ids ON public.organization_memberships;
DROP FUNCTION public.bloem_prune_membership_library_ids(integer[]);
DO $$
DECLARE name text; definition text;
BEGIN
 FOREACH name IN ARRAY ARRAY['fence_legacy_user_policy_write','guard_membership_policy_write'] LOOP
  definition:=pg_get_functiondef(format('public.%I()',name)::regprocedure);
  EXECUTE replace(definition,$repair$
    IF TG_OP='UPDATE'
       AND (to_jsonb(NEW)-'library_ids'-'updated_at'-'admin_revision') IS NOT DISTINCT FROM (to_jsonb(OLD)-'library_ids'-'updated_at'-'admin_revision')
       AND NEW.library_ids IS DISTINCT FROM OLD.library_ids
       AND NEW.library_ids IS NOT DISTINCT FROM public.existing_library_ids(OLD.library_ids) THEN
        RETURN NEW;
    END IF;
$repair$,'');
 END LOOP;
END $$;
-- +goose StatementEnd`
