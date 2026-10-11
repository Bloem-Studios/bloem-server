package bloemtestdb

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIsTestDatabaseName(t *testing.T) {
	for name, want := range map[string]bool{
		"bloem_test":                  true,
		"a6_test":                     true,
		"bloem_contract_scenarios_ci": true,
		"TEST_upper":                  true,
		"bloem":                       false,
		"postgres":                    false,
		"production":                  false,
		"silo_cinema":                 false,
	} {
		if got := IsTestDatabaseName(name); got != want {
			t.Errorf("IsTestDatabaseName(%q) = %t, want %t", name, got, want)
		}
	}
}

// Prepare refuses before it connects, so a production DSN never gets as far
// as a migration or the shim.
func TestPrepareRefusesNonTestDatabase(t *testing.T) {
	err := Prepare(context.Background(), "postgres://nobody:nobody@127.0.0.1:1/bloem?sslmode=disable", Options{Recreate: true})
	if !errors.Is(err, ErrNotATestDatabase) {
		t.Fatalf("Prepare(bloem) = %v, want ErrNotATestDatabase", err)
	}
}

func TestSuiteIsolationRefusesNonTestDatabase(t *testing.T) {
	_, _, err := isolateSuite(t.Context(), "postgres://nobody:nobody@127.0.0.1:1/bloem?sslmode=disable")
	if !errors.Is(err, ErrNotATestDatabase) {
		t.Fatalf("isolateSuite(bloem) = %v, want ErrNotATestDatabase", err)
	}
}

func TestSuiteDSNOverridesParentDatabase(t *testing.T) {
	for _, parent := range []string{
		"postgres://nobody:fixture@127.0.0.1:65419/parent_test?sslmode=disable",
		"postgresql://nobody:fixture@127.0.0.1:65419/parent_test?sslmode=disable&dbname=override_test",
		"host=127.0.0.1 port=65419 user=nobody password=fixture dbname=parent_test sslmode=disable",
	} {
		t.Run(parent, func(t *testing.T) {
			dsn, err := suiteDSN(parent, "bloem_suite_test_child")
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := pgx.ParseConfig(dsn)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Database != "bloem_suite_test_child" || cfg.Port != 65419 || cfg.User != "nobody" || cfg.Password != "fixture" || cfg.TLSConfig != nil {
				t.Fatal("child DSN changed connection settings or kept the parent database")
			}
		})
	}
}
