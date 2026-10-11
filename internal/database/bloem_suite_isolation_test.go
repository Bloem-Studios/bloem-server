package database_test

import (
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

var bloemSuiteParentDSN = os.Getenv("SILO_TEST_DATABASE_URL")

// Migration rehearsals must not change the schema used by other packages.
func TestBloemMigrationSuiteKeepsParentSchema(t *testing.T) {
	if bloemSuiteParentDSN == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	child, err := pgx.Connect(t.Context(), os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Close(t.Context()) }()
	parent, err := pgx.Connect(t.Context(), bloemSuiteParentDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(t.Context()) }()
	if _, err := child.Exec(t.Context(), "CREATE TABLE bloem_migration_suite_isolation_probe (id integer)"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = child.Exec(t.Context(), "DROP TABLE bloem_migration_suite_isolation_probe") }()
	var absent bool
	if err := parent.QueryRow(t.Context(), "SELECT to_regclass('public.bloem_migration_suite_isolation_probe') IS NULL").Scan(&absent); err != nil {
		t.Fatal(err)
	}
	if !absent {
		t.Fatal("migration suite changes are visible in the shared test database")
	}
}
