package database

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

const bloemPolicyLibraryIDsVersion int64 = 20260919095615
const bloemPolicyLibraryIDsPredecessor int64 = 20260917214744

var bloemPolicyLibraryTables = []string{
	"users", "access_groups", "entitlement_template_revisions", "entitlement_policy_cohort_revisions",
	"invitations", "organization_memberships", "legacy_user_policy_rollback_snapshot", "membership_policy_rollback_snapshot",
}

func TestBloemPolicyLibraryIDsMigration(t *testing.T) {
	for _, finalized := range []bool{false, true} {
		t.Run(fmt.Sprintf("finalized=%t", finalized), func(t *testing.T) {
			ctx := t.Context()
			fixture := newMembershipPolicyFixture(t)
			pool := fixture.pool
			provider, err := newMigrationProvider(pool, migrations.FS, "sql")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = provider.Close() }()
			if _, err := provider.UpTo(ctx, bloemPolicyLibraryIDsPredecessor); err != nil {
				t.Fatal(err)
			}
			pool.Reset()
			if finalized {
				if changed, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil || !changed {
					t.Fatalf("finalize policy authority: changed=%t err=%v", changed, err)
				}
				pool.Reset()
			}
			// These unassigned groups distinguish inherited NULL from explicit
			// denial (empty), and retain an array's bounds and NULL elements.
			if _, err := pool.Exec(ctx, `INSERT INTO access_groups(organization_id,name,library_ids) VALUES
				($1,'Width NULL',NULL),($1,'Width empty','{}'::integer[]),
				($1,'Width bounds','[0:2]={7,NULL,2147483647}'::integer[])`, fixture.defaultOrgID); err != nil {
				t.Fatal(err)
			}
			// Exercise WHEN dependencies too, and preserve operator-selected
			// enabled modes. These no-op test triggers do not replace any guard.
			if _, err := pool.Exec(ctx, `CREATE FUNCTION public.bloem_width_probe() RETURNS trigger
				LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`); err != nil {
				t.Fatal(err)
			}
			for name, mode := range map[string]string{"always": "ENABLE ALWAYS", "replica": "ENABLE REPLICA", "disabled": "DISABLE"} {
				if _, err := pool.Exec(ctx, fmt.Sprintf(`CREATE TRIGGER bloem_width_%s BEFORE UPDATE ON public.access_groups
					FOR EACH ROW WHEN (OLD.library_ids IS DISTINCT FROM NEW.library_ids) EXECUTE FUNCTION public.bloem_width_probe();
					ALTER TABLE public.access_groups %s TRIGGER bloem_width_%s`, name, mode, name)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, `COMMENT ON TRIGGER bloem_width_always ON public.access_groups IS 'operator''s annotation: preserve this'`); err != nil {
				t.Fatal(err)
			}
			before := bloemPolicyLibraryState(t, pool)
			for _, step := range []struct {
				name string
				up   bool
			}{{"up", true}, {"down", false}, {"up-again", true}} {
				t.Run(step.name, func(t *testing.T) {
					if step.up {
						_, err = provider.UpTo(ctx, bloemPolicyLibraryIDsVersion)
					} else {
						err = MigrateDownTo(ctx, pool, migrations.FS, "sql", bloemPolicyLibraryIDsPredecessor)
					}
					if err != nil {
						t.Fatal(err)
					}
					pool.Reset()
					wantType := "integer[]"
					if step.up {
						wantType = "bigint[]"
					}
					assertBloemPolicyLibraryTypes(t, pool, finalized, wantType)
					if after := bloemPolicyLibraryState(t, pool); !slices.Equal(before, after) {
						t.Fatal("migration changed policy data, array shape, authority, revisions or trigger definitions/modes")
					}
					// The restored writer fence must still reject unmarked policy
					// changes, regardless of authority phase or parameter width.
					tx, err := pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback(context.Background()) }()
					if _, err := tx.Exec(ctx, `SELECT set_config('bloem.membership_policy_writer','',true)`); err != nil {
						t.Fatal(err)
					}
					_, err = tx.Exec(ctx, `UPDATE organization_memberships SET max_streams=99 WHERE account_id=$1`, fixture.accountID)
					assertMembershipPolicyError(t, err, "membership_policy_not_finalized")
				})
			}

			// The last table in the migration must veto Down before ANY column
			// narrows. Snapshots are part of lossless rollback, not disposable
			// caches. Test both int4 boundaries, and preserve the failed state.
			for _, id := range []int64{2147483648, -2147483649} {
				t.Run(fmt.Sprintf("refuse-%d", id), func(t *testing.T) {
					if tag, err := pool.Exec(ctx, `UPDATE membership_policy_rollback_snapshot SET library_ids=$1`, []int64{id}); err != nil || tag.RowsAffected() != 2 {
						t.Fatalf("seed snapshot range: %v", err)
					}
					beforeFailure := bloemPolicyLibraryState(t, pool)
					err := MigrateDownTo(ctx, pool, migrations.FS, "sql", bloemPolicyLibraryIDsPredecessor)
					if err == nil || !strings.Contains(err.Error(), "membership_policy_rollback_snapshot.library_ids contains an ID outside integer range") {
						t.Fatalf("Down must refuse loss of snapshot ID: %v", err)
					}
					pool.Reset()
					assertBloemPolicyLibraryTypes(t, pool, finalized, "bigint[]")
					if after := bloemPolicyLibraryState(t, pool); !slices.Equal(beforeFailure, after) {
						t.Fatal("refused Down changed data or guards")
					}
					var applied bool
					if err := pool.QueryRow(ctx, `SELECT is_applied FROM goose_db_version WHERE version_id=$1 ORDER BY id DESC LIMIT 1`, bloemPolicyLibraryIDsVersion).Scan(&applied); err != nil || !applied {
						t.Fatalf("refused Down changed migration history: applied=%t err=%v", applied, err)
					}
				})
			}
		})
	}
}

func assertBloemPolicyLibraryTypes(t *testing.T, pool *pgxpool.Pool, finalized bool, want string) {
	t.Helper()
	for _, table := range bloemPolicyLibraryTables {
		column := "library_ids"
		if table == "users" && finalized {
			column = "rollback_membership_library_ids"
		}
		var got string
		if err := pool.QueryRow(t.Context(), `SELECT format_type(atttypid,atttypmod) FROM pg_attribute
			WHERE attrelid=$1::regclass AND attname=$2 AND NOT attisdropped`, "public."+table, column).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s.%s type=%s, want %s", table, column, got, want)
		}
	}
}

func bloemPolicyLibraryState(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	ctx := t.Context()
	var result []string
	for _, table := range append(slices.Clone(bloemPolicyLibraryTables), "membership_policy_authority") {
		var rows string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]')::text FROM public.`+pgxIdentifier(table)+` t`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		result = append(result, rows)
	}
	// JSON arrays omit SQL dimensions, so compare the literal representation as
	// well. NULL and '{}' also remain distinct in this ordered text aggregate.
	var arrays, triggers string
	if err := pool.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(name,library_ids::text) ORDER BY name)::text FROM access_groups`).Scan(&arrays); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled,obj_description(t.oid,'pg_trigger'))
		ORDER BY c.relname,t.tgname)::text FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid
		WHERE c.relnamespace='public'::regnamespace AND c.relname=ANY($1) AND NOT t.tgisinternal`, bloemPolicyLibraryTables).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	return append(result, arrays, triggers)
}
