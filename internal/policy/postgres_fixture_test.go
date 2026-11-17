package policy

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
)

// TestMain runs the package on its own prepared database
// (bloemtestdb.RunIsolatedSuite), as the pgstore package does: Silo's database
// tests here, such as TestViewerResolverBatchedReadsMatchEachDB, seed accounts
// and profiles directly and need the fixture-compatibility triggers, which the
// tenancy job's bare migrated database does not carry. Silo's policy-store
// tests (store_test.go) reset the policy tables of that database only.
func TestMain(m *testing.M) {
	os.Exit(bloemtestdb.RunIsolatedSuite(m.Run))
}

// TestPolicyStoreTestsRunOnMigratedDatabase keeps Silo's policy-store tests
// from skipping silently: newPolicyStoreTest skips when policy_documents is
// missing, which on a prepared database would mean a broken preparation.
func TestPolicyStoreTestsRunOnMigratedDatabase(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()
	var policyTable *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.policy_documents')::text`).Scan(&policyTable); err != nil {
		t.Fatalf("inspect policy-store test database: %v", err)
	}
	if policyTable == nil || *policyTable != "policy_documents" {
		t.Fatalf("policy_documents table = %v, want the migrated schema", policyTable)
	}
}

func TestPolicyOrdinaryCIWithoutDatabaseRunsNonDatabaseTest(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate policy test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestTenantFactsFromContextRequiresCompleteResolvedContext$", "-test.v")
	command.Env = policyTestEnvironmentWithout("SILO_TEST_DATABASE_URL", "SILO_REQUIRE_TEST_DATABASE", "CI")
	command.Env = append(command.Env, "CI=true")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ordinary CI policy subprocess without database = %v, output=%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestTenantFactsFromContextRequiresCompleteResolvedContext") {
		t.Fatalf("ordinary CI did not execute named non-database policy test; output=%s", output)
	}
}

func TestPolicyRequiredDatabaseSignalFailsWithoutURL(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate policy test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestPolicyStoreDocumentVersionCRUD$", "-test.v")
	command.Env = policyTestEnvironmentWithout("SILO_TEST_DATABASE_URL", "SILO_REQUIRE_TEST_DATABASE", "CI")
	command.Env = append(command.Env, "SILO_REQUIRE_TEST_DATABASE=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("required policy PostgreSQL subprocess without database passed; output=%s", output)
	}
	if !strings.Contains(string(output), "SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1") {
		t.Fatalf("required policy PostgreSQL subprocess failure = %v, output=%s", err, output)
	}
}

func policyTestEnvironmentWithout(names ...string) []string {
	blocked := make(map[string]struct{}, len(names))
	for _, name := range names {
		blocked[name] = struct{}{}
	}
	filtered := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, skip := blocked[name]; !skip {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
