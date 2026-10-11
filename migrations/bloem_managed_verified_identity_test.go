package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The managed-tracking default-organization migration this one builds on.
const managedDefaultOrganizationVersion = 20261011175820

const managedIdentityIssuer = "https://idp.example.test/application/o/silo/"

func managedIdentityVersion(t *testing.T) int64 {
	t.Helper()
	matches, err := fs.Glob(FS, "sql/*_bloem_managed_tracking_verified_identity.sql")
	if err != nil || len(matches) != 1 {
		t.Fatalf("verified identity migration missing: %v %v", matches, err)
	}
	version, err := strconv.ParseInt(strings.Split(strings.TrimPrefix(matches[0], "sql/"), "_")[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

// managedIdentityDB is a disposable database at the version before the
// verified-identity migration, holding enrolled accounts:
//
//	93001 one verified identity, its legacy row has an empty issuer column
//	93002 no sign-in identity
//	93003 an issuer column that disagrees with the token's issuer
//	93004 an identity only in a disabled installation
func managedIdentityDB(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("test database required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "postgres"
	admin := stdlib.OpenDB(*cfg)
	name := fmt.Sprintf("managed_identity_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), managedDefaultOrganizationVersion); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id,username,role) VALUES(93001,'identified','user'),(93002,'plain','user'),(93003,'mismatched','user'),(93004,'disabled','user')`,
		`INSERT INTO organization_memberships(organization_id,account_id,status,legacy_role)
		 SELECT o.id,u.id,'active','user' FROM organizations o CROSS JOIN users u WHERE o.is_default AND u.id BETWEEN 93001 AND 93004`,
		`INSERT INTO user_profiles(id,user_id,name,organization_id,access_group_id,is_primary)
		 SELECT 'managed-'||u.id||'-'||n,u.id,'Profile '||n,o.id,(SELECT g.id FROM access_groups g WHERE g.organization_id=o.id AND g.is_default),n=1
		 FROM organizations o CROSS JOIN users u CROSS JOIN generate_series(1,2) n WHERE o.is_default AND u.id BETWEEN 93001 AND 93004`,
		`INSERT INTO plugin_installations(id,plugin_id,version,install_path,enabled) VALUES
		 (93001,'silo.auth.oidc','1.0.0','fixture',true),(93002,'silo.auth.oidc','1.0.0','fixture',false),(93003,'bloem.pastime','0.1.3','fixture',true)`,
		`INSERT INTO plugin_auth_identities(plugin_installation_id,external_subject,user_id,issuer) VALUES
		 (93001,'` + managedIdentityIssuer + `|subject-93001',93001,''),
		 (93001,'` + managedIdentityIssuer + `|subject-93003',93003,'https://other.example.test/'),
		 (93002,'` + managedIdentityIssuer + `|subject-93004',93004,'')`,
		`INSERT INTO bloem_managed_grants(installation_id,tenant_id) SELECT 93003,id FROM organizations WHERE is_default`,
		`INSERT INTO bloem_managed_snapshots(id,installation_id,generation,watermark) SELECT gen_random_uuid(),93003,1,revision FROM bloem_managed_clock`,
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db, provider
}

func managedRevisions(t *testing.T, db *sql.DB) map[int]int64 {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT account_id,max(account_revision) FROM bloem_managed_profiles WHERE account_id BETWEEN 93001 AND 93004 GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]int64{}
	for rows.Next() {
		var account int
		var revision int64
		if err := rows.Scan(&account, &revision); err != nil {
			t.Fatal(err)
		}
		out[account] = revision
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("projected accounts = %v", out)
	}
	return out
}

func managedIdentities(t *testing.T, db *sql.DB) map[int]string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT account_id,string_agg(DISTINCT identity_issuer||'|'||identity_subject,',') FROM bloem_managed_profiles WHERE account_id BETWEEN 93001 AND 93004 GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var account int
		var identity string
		if err := rows.Scan(&account, &identity); err != nil {
			t.Fatal(err)
		}
		out[account] = identity
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func managedObjects(t *testing.T, db *sql.DB) (columns, functions, triggers, index int) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM information_schema.columns WHERE table_name='bloem_managed_profiles' AND column_name IN ('identity_issuer','identity_subject')),
	 (SELECT count(*) FROM pg_proc WHERE proname IN ('bloem_managed_verified_identity','bloem_managed_refresh_identity','bloem_managed_auth_identity_changed','bloem_managed_auth_installation_changed')),
	 (SELECT count(*) FROM pg_trigger WHERE tgname IN ('bloem_managed_auth_identities','bloem_managed_auth_identities_update','bloem_managed_auth_installations')),
	 (SELECT count(*) FROM pg_indexes WHERE indexname='bloem_managed_profiles_account')`).Scan(&columns, &functions, &triggers, &index); err != nil {
		t.Fatal(err)
	}
	return
}

func TestManagedVerifiedIdentityMigrationPostgres(t *testing.T) {
	db, provider := managedIdentityDB(t)
	version := managedIdentityVersion(t)
	before := managedRevisions(t, db)
	if _, err := provider.UpTo(t.Context(), version); err != nil {
		t.Fatal(err)
	}
	if columns, functions, triggers, index := managedObjects(t, db); columns != 2 || functions != 4 || triggers != 3 || index != 1 {
		t.Fatalf("up objects = %d/%d/%d/%d", columns, functions, triggers, index)
	}
	identities := managedIdentities(t, db)
	if identities[93001] != managedIdentityIssuer+"|subject-93001" {
		t.Fatalf("backfilled identity = %q", identities[93001])
	}
	for _, account := range []int{93002, 93003, 93004} {
		if identities[account] != "|" {
			t.Fatalf("account %d backfilled %q", account, identities[account])
		}
	}
	upgraded := managedRevisions(t, db)
	if upgraded[93001] <= before[93001] {
		t.Fatal("backfill published no revision for the identified account")
	}
	for _, account := range []int{93002, 93003, 93004} {
		if upgraded[account] != before[account] {
			t.Fatalf("backfill republished account %d without an identity", account)
		}
	}
	var changes, snapshots int
	if err := db.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM bloem_managed_changes WHERE account_id=93001 AND revision=$1),
	 (SELECT count(*) FROM bloem_managed_snapshots)`, upgraded[93001]).Scan(&changes, &snapshots); err != nil {
		t.Fatal(err)
	}
	if changes != 1 || snapshots != 0 {
		t.Fatalf("changes=%d snapshots=%d", changes, snapshots)
	}

	// The triggers keep the projection current after the upgrade.
	if _, err := db.ExecContext(t.Context(), `INSERT INTO plugin_auth_identities(plugin_installation_id,external_subject,user_id) VALUES(93001,'`+managedIdentityIssuer+`|subject-93002',93002)`); err != nil {
		t.Fatal(err)
	}
	if got := managedIdentities(t, db)[93002]; got != managedIdentityIssuer+"|subject-93002" {
		t.Fatalf("linked identity = %q", got)
	}
	identified := managedRevisions(t, db)

	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if columns, functions, triggers, index := managedObjects(t, db); columns != 0 || functions != 0 || triggers != 0 || index != 0 {
		t.Fatalf("down left objects %d/%d/%d/%d", columns, functions, triggers, index)
	}
	cleared := managedRevisions(t, db)
	for _, account := range []int{93001, 93002} {
		if cleared[account] <= identified[account] {
			t.Fatalf("down did not republish account %d with its identity cleared", account)
		}
	}
	for _, account := range []int{93003, 93004} {
		if cleared[account] != identified[account] {
			t.Fatalf("down republished account %d that had no identity", account)
		}
	}
	var body string
	if err := db.QueryRowContext(t.Context(), `SELECT pg_get_functiondef('bloem_managed_refresh_account(integer)'::regprocedure)`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "identity") || strings.Contains(body, "advisory") || !strings.Contains(body, "o.is_default AND o.status='initializing'") {
		t.Fatalf("down did not restore the previous refresh body:\n%s", body)
	}
	// The previous body works without the identity columns.
	if _, err := db.ExecContext(t.Context(), `UPDATE users SET username='identified-renamed' WHERE id=93001`); err != nil {
		t.Fatal(err)
	}

	// Up again after a rollback.
	if _, err := provider.UpTo(t.Context(), version); err != nil {
		t.Fatal(err)
	}
	if got := managedIdentities(t, db); got[93001] != managedIdentityIssuer+"|subject-93001" || got[93002] != managedIdentityIssuer+"|subject-93002" {
		t.Fatalf("re-applied identities = %v", got)
	}
}
