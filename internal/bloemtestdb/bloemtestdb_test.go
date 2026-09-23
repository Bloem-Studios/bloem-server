package bloemtestdb

import (
	"context"
	"errors"
	"testing"
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
