package policy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var (
	policyStoreTestDatabaseConfig *pgxpool.Config
	policyStoreTestDatabaseName   string
)

func TestMain(m *testing.M) {
	dsn := strings.TrimSpace(os.Getenv("SILO_TEST_DATABASE_URL"))
	if dsn == "" {
		if strings.TrimSpace(os.Getenv("SILO_REQUIRE_TEST_DATABASE")) == "1" {
			_, _ = fmt.Fprintln(os.Stderr, "SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
			os.Exit(1)
		}
		os.Exit(m.Run())
	}

	var cleanup func() error
	var err error
	policyStoreTestDatabaseConfig, policyStoreTestDatabaseName, cleanup, err = preparePolicyStoreTestDatabase(context.Background(), dsn)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "prepare policy-store test database: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	if err := cleanup(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up policy-store test database: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func TestPolicyStoreUsesDisposableMigratedDatabase(t *testing.T) {
	ctx := context.Background()
	pool, _ := newPolicyStoreTest(t, ctx)

	var (
		currentDatabase string
		policyTable     *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT current_database(), to_regclass('public.policy_documents')::text`,
	).Scan(&currentDatabase, &policyTable); err != nil {
		t.Fatalf("inspect policy-store test database: %v", err)
	}
	maintenanceConfig, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	if currentDatabase == maintenanceConfig.ConnConfig.Database {
		t.Fatalf("policy-store test database = maintenance database %q", currentDatabase)
	}
	if currentDatabase != policyStoreTestDatabaseName || !strings.HasPrefix(currentDatabase, "bloem_policy_store_") {
		t.Fatalf("policy-store test database = %q, want exact disposable child %q", currentDatabase, policyStoreTestDatabaseName)
	}
	if policyTable == nil || *policyTable != "policy_documents" {
		t.Fatalf("policy_documents table = %v, want migrated child schema", policyTable)
	}
}

func TestPolicyOrdinaryCIWithoutDatabaseRunsNonDatabaseTest(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate policy test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestLockedCapabilitiesRejectHTTP$", "-test.v")
	command.Env = policyTestEnvironmentWithout("SILO_TEST_DATABASE_URL", "SILO_REQUIRE_TEST_DATABASE", "CI")
	command.Env = append(command.Env, "CI=true")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("ordinary CI policy subprocess without database = %v, output=%s", err, output)
	}
	if !strings.Contains(string(output), "--- PASS: TestLockedCapabilitiesRejectHTTP") {
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

func preparePolicyStoreTestDatabase(
	ctx context.Context,
	dsn string,
) (*pgxpool.Config, string, func() error, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", nil, fmt.Errorf("generate disposable database name: %w", err)
	}
	name := "bloem_policy_store_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse maintenance database URL: %w", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		return nil, "", nil, fmt.Errorf("connect maintenance database: %w", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		return nil, "", nil, fmt.Errorf("create disposable database %q: %w", name, err)
	}

	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, "", nil, fmt.Errorf("parse disposable database URL: %w", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, "", nil, fmt.Errorf("connect disposable database %q: %w", name, err)
	}
	if err := migratePolicyStoreTestDatabase(ctx, pool); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		return nil, "", nil, fmt.Errorf("migrate disposable database %q: %w", name, err)
	}
	pool.Close()

	cleanup := func() error {
		defer admin.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			return fmt.Errorf("drop disposable database %q: %w", name, err)
		}
		var exists bool
		if err := admin.QueryRow(cleanupCtx, `
			SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); err != nil {
			return fmt.Errorf("verify disposable database %q cleanup: %w", name, err)
		}
		if exists {
			return fmt.Errorf("disposable database %q still exists after cleanup", name)
		}
		return nil
	}
	return testConfig.Copy(), name, cleanup, nil
}

func migratePolicyStoreTestDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	migrationFS, err := fs.Sub(migrations.FS, "sql")
	if err != nil {
		return fmt.Errorf("open embedded SQL migrations: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		stdlib.OpenDBFromPool(pool),
		migrationFS,
		goose.WithTableName("public.goose_db_version"),
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		return fmt.Errorf("create Goose provider: %w", err)
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply embedded SQL migrations: %w", err)
	}
	return nil
}
