package migrations

import (
	"fmt"
	"strings"
)

func adaptBloemProfileLimits(raw string) (string, error) {
	const group = `ALTER TABLE access_groups
    ADD COLUMN max_profiles integer NOT NULL DEFAULT 5
        CONSTRAINT access_groups_max_profiles_min_check CHECK (max_profiles >= 1);`
	const users = `ALTER TABLE users
    ALTER COLUMN max_profiles DROP NOT NULL,
    ALTER COLUMN max_profiles DROP DEFAULT;

UPDATE users SET max_profiles = NULL WHERE max_profiles = 5;`
	start := strings.Index(raw, "UPDATE users u\nSET max_profiles = COALESCE(")
	end := -1
	if start >= 0 {
		if n := strings.Index(raw[start:], "-- +goose StatementBegin\nCREATE OR REPLACE FUNCTION"); n >= 0 {
			end = start + n
		}
	}
	if strings.Count(raw, group) != 1 || strings.Count(raw, users) != 1 || start < 0 || end < 0 {
		return "", fmt.Errorf("Bloem profile-limit adapter: upstream statements changed")
	}
	raw = raw[:start] + bloemProfileLimitTransition(false) + "\n" + bloemProfileLimitTrigger(false) + "\n" + raw[end:]
	raw = strings.Replace(raw, group, `-- Bloem already owns this column, including managed zero-profile entitlements.
ALTER TABLE access_groups ALTER COLUMN max_profiles SET DEFAULT 5;
UPDATE access_groups SET max_profiles=5 WHERE max_profiles=0 AND managed_template_key IS NULL;`, 1)
	raw = strings.Replace(raw, users, bloemProfileLimitTransition(true)+"\n"+bloemProfileLimitTrigger(true), 1)
	raw = strings.Replace(raw, "ALTER TABLE access_groups DROP COLUMN max_profiles;", "-- Retain Bloem's pre-existing max_profiles column.", 1)
	return raw, nil
}

func bloemProfileLimitTransition(up bool) string {
	alter := "DROP NOT NULL"
	def := "DROP DEFAULT"
	update := `SET max_profiles=NULL WHERE max_profiles=5`
	if !up {
		alter = "SET NOT NULL"
		def = "SET DEFAULT 5"
		update = `SET max_profiles=COALESCE((SELECT NULLIF(g.max_profiles,0) FROM access_groups g WHERE g.id=t.access_group_id),5) WHERE max_profiles IS NULL`
	}
	// Both policy guards must be suspended inside this transaction while the
	// representation changes, including frozen compatibility databases. Keep
	// their exact enabled modes; they are restored before any writer can run.
	before := `-- +goose StatementBegin
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
`
	// Make every authority and rollback snapshot accept inherited limits before
	// mirror triggers can copy a NULL from the account.
	if up {
		before += ` EXECUTE format('ALTER TABLE users ALTER COLUMN %I DROP NOT NULL, ALTER COLUMN %I DROP DEFAULT',account_column,account_column);
 FOR tbl IN SELECT unnest(ARRAY['organization_memberships','legacy_user_policy_rollback_snapshot','membership_policy_rollback_snapshot']) LOOP
  EXECUTE format('ALTER TABLE %I ALTER COLUMN max_profiles DROP NOT NULL, ALTER COLUMN max_profiles DROP DEFAULT',tbl);
 END LOOP;
 EXECUTE format('UPDATE users SET %I=NULL WHERE %I=5',account_column,account_column);
`
	} else {
		before += ` EXECUTE format('UPDATE users u SET %I=COALESCE((SELECT NULLIF(g.max_profiles,0) FROM access_groups g WHERE g.id=(to_jsonb(u)->>''access_group_id'')::bigint AND u.role<>''admin''),5) WHERE %I IS NULL',account_column,account_column);
 EXECUTE format('ALTER TABLE users ALTER COLUMN %I SET DEFAULT 5, ALTER COLUMN %I SET NOT NULL',account_column,account_column);
`
	}
	before += fmt.Sprintf(` FOR tbl IN SELECT unnest(ARRAY['organization_memberships','legacy_user_policy_rollback_snapshot','membership_policy_rollback_snapshot']) LOOP
  EXECUTE format('UPDATE %%I t %s',tbl);
  EXECUTE format('ALTER TABLE %%I ALTER COLUMN max_profiles %s, ALTER COLUMN max_profiles %s',tbl);
 END LOOP;
 FOR guard IN SELECT * FROM jsonb_to_recordset(guards) AS x(tbl text,tgname text,tgenabled text) LOOP
  EXECUTE format('ALTER TABLE %%s %%s TRIGGER %%I',guard.tbl,CASE guard.tgenabled WHEN 'D' THEN 'DISABLE' WHEN 'A' THEN 'ENABLE ALWAYS' WHEN 'R' THEN 'ENABLE REPLICA' ELSE 'ENABLE' END,guard.tgname);
 END LOOP;
END $$;
-- +goose StatementEnd`, update, alter, def)
	return before
}

// Adapt only the owned entitlement trigger's limit calculation. The upstream
// account override takes precedence; a managed entitlement remains a ceiling.
func bloemProfileLimitTrigger(up bool) string {
	const previous = `effective_limit := CASE
        WHEN membership_limit > 0 AND group_limit > 0 THEN LEAST(membership_limit, group_limit)
        WHEN membership_limit > 0 THEN membership_limit
        WHEN group_limit > 0 THEN group_limit
        ELSE 0
    END;`
	const current = `effective_limit := COALESCE(membership_limit, NULLIF(group_limit,0),5);
    IF group_managed THEN
        effective_limit := LEAST(effective_limit,GREATEST(group_limit,1));
    END IF;`
	old, new := previous, current
	if !up {
		old, new = current, previous
	}
	return fmt.Sprintf(`-- +goose StatementBegin
DO $adapter$
DECLARE definition text;
BEGIN
 definition:=pg_get_functiondef('public.enforce_user_profile_entitlement_limit()'::regprocedure);
 IF strpos(definition,$old$%s$old$)=0 THEN RAISE EXCEPTION 'Bloem profile-limit trigger changed'; END IF;
 EXECUTE replace(definition,$old$%s$old$,$new$%s$new$);
END $adapter$;
-- +goose StatementEnd`, old, old, new)
}
