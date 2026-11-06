package bloemtestdb

import (
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Upstream scanner and catalog fixtures delete libraries directly. The test
// bootstrap must supply entitlement bookkeeping without relaxing the FK.
func TestFixtureLibraryTeardownReleasesOnlyItsEntitlements(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var setting string
	if err := tx.QueryRow(ctx, `SELECT current_setting('bloem.fixture_compat',true)`).Scan(&setting); err != nil || setting != "on" {
		t.Fatalf("fixture compatibility is not enabled: setting=%q err=%v", setting, err)
	}
	var deletedID, keptID int
	for i, target := range []*int{&deletedID, &keptID} {
		if err := tx.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies',$1) RETURNING id`, []string{"Fixture teardown target", "Fixture teardown survivor"}[i]).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	var entitlements int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM organization_entitlements WHERE media_folder_id=ANY($1::int[])`, []int{deletedID, keptID}).Scan(&entitlements); err != nil || entitlements != 2 {
		t.Fatalf("default entitlements=%d err=%v", entitlements, err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL bloem.fixture_compat='off'; SAVEPOINT restricted_delete`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, deletedID)
	// PostgreSQL 18 reports an ON DELETE RESTRICT violation as
	// restrict_violation (23001); earlier releases report foreign_key_violation.
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || (constraint.Code != "23503" && constraint.Code != "23001") || constraint.ConstraintName != "organization_entitlements_media_folder_fkey" {
		t.Fatalf("compatibility off must retain entitlement restriction: %v", err)
	}
	if _, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT restricted_delete; SET LOCAL bloem.fixture_compat='on'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM media_folders WHERE id=$1`, deletedID); err != nil {
		t.Fatalf("upstream bare library teardown failed: %v", err)
	}
	var deleted, kept int
	if err := tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE media_folder_id=$1),count(*) FILTER(WHERE media_folder_id=$2) FROM organization_entitlements WHERE media_folder_id=ANY($3::int[])`, deletedID, keptID, []int{deletedID, keptID}).Scan(&deleted, &kept); err != nil || deleted != 0 || kept != 1 {
		t.Fatalf("entitlements after teardown: deleted=%d kept=%d err=%v", deleted, kept, err)
	}
}

// A copy of a test database, or one restored from its dump, has the
// triggers but not the database setting: the shim arms itself there by the
// database's name, and an explicit off still disarms it.
func TestFixtureCompatArmsWithoutTheSettingInATestDatabase(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var name string
	if err := tx.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil || !IsTestDatabaseName(name) {
		t.Fatalf("database %q (%v) is not a test database", name, err)
	}
	for _, c := range []struct {
		setting string
		armed   bool
	}{{"on", true}, {"", true}, {"off", false}} {
		var armed bool
		if err := tx.QueryRow(ctx, `SELECT set_config('bloem.fixture_compat', $1, true) IS NOT NULL AND public.bloem_fixture_compat_armed()`, c.setting).Scan(&armed); err != nil {
			t.Fatal(err)
		}
		if armed != c.armed {
			t.Errorf("bloem.fixture_compat=%q: armed=%v, want %v", c.setting, armed, c.armed)
		}
	}
	// Unset, a profile written without an organization is placed in the
	// default one.
	if _, err := tx.Exec(ctx, `SELECT set_config('bloem.fixture_compat', '', true)`); err != nil {
		t.Fatal(err)
	}
	var userID int
	if err := tx.QueryRow(ctx, `INSERT INTO users (role, username) VALUES ('user', 'fixture-compat-unset') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var org *string
	if err := tx.QueryRow(ctx, `INSERT INTO user_profiles (id, user_id, name) VALUES ('fixture-compat-unset', $1, 'Unset') RETURNING organization_id::text`, userID).Scan(&org); err != nil || org == nil {
		t.Fatalf("profile without an organization: org=%v err=%v", org, err)
	}
}
