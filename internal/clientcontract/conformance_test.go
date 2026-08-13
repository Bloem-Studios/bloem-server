package clientcontract

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDisposableDatabaseNamesAreUniqueAndGuarded(t *testing.T) {
	first, err := newDisposableDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newDisposableDatabaseName()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("generated duplicate database name %q", first)
	}
	if !regexp.MustCompile(`^vondel_client_contract_[a-f0-9]{16}$`).MatchString(first) {
		t.Fatalf("database name %q is outside the cleanup namespace", first)
	}
	for _, unsafe := range []string{"postgres", "vondel", "vondel_client_contract_", "vondel_client_contract_bad-name", ""} {
		if err := validateDisposableDatabaseName(unsafe); err == nil {
			t.Errorf("unsafe database name %q passed cleanup validation", unsafe)
		}
	}
}

func TestEnvironmentOverridesDoNotLeaveDuplicateSecretsOrDatabaseURLs(t *testing.T) {
	env := environmentWithOverrides([]string{
		"PATH=/fixture/bin",
		"DATABASE_URL=postgres://wrong.invalid/production",
		"SECRET_KEY=wrong-secret",
	}, map[string]string{
		"DATABASE_URL": "postgres://fixture.invalid/disposable",
		"SECRET_KEY":   "invented-secret",
	})
	want := map[string]string{
		"DATABASE_URL": "postgres://fixture.invalid/disposable",
		"SECRET_KEY":   "invented-secret",
	}
	counts := make(map[string]int)
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if expected, ok := want[key]; ok {
			counts[key]++
			if value != expected {
				t.Errorf("%s = %q, want %q", key, value, expected)
			}
		}
	}
	for key := range want {
		if counts[key] != 1 {
			t.Errorf("%s entries = %d, want 1", key, counts[key])
		}
	}
}

func TestDisposableDatabaseMigratesSeedsRunsAndDrops(t *testing.T) {
	dsn := os.Getenv("SILO_CLIENT_CONTRACT_ADMIN_URL")
	if dsn == "" {
		t.Skip("SILO_CLIENT_CONTRACT_ADMIN_URL is not set")
	}
	contractsDir := contractsRepository(t)
	ctx := context.Background()
	db := createDisposableDatabase(t, ctx, dsn)
	name := db.name
	admin := db.admin
	t.Cleanup(func() {
		db.cleanup(t, context.Background())
		db.admin.Close()
	})

	migrateAndSeed(t, ctx, db.pool)
	server := startVondelServer(t, db.pool)
	setupFixtureAccount(t, server.URL)
	seedAccountScopedFixtures(t, ctx, db.pool)
	runContractsCLI(t, contractsDir, server.URL)

	db.cleanup(t, context.Background())
	if databaseExists(t, context.Background(), admin, name) {
		t.Fatalf("generated database %q still exists after cleanup", name)
	}
}

func TestOfficialSiloPinnedBaseline(t *testing.T) {
	if os.Getenv("VONDEL_RUN_OFFICIAL_SILO_BASELINE") != "1" {
		t.Skip("set VONDEL_RUN_OFFICIAL_SILO_BASELINE=1 to run pinned official-Silo compatibility")
	}
	dsn := os.Getenv("SILO_CLIENT_CONTRACT_ADMIN_URL")
	if dsn == "" {
		t.Skip("SILO_CLIENT_CONTRACT_ADMIN_URL is not set")
	}
	runOfficialSiloBaseline(t, context.Background(), dsn, contractsRepository(t))
}
