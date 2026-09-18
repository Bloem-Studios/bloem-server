package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

// NewBloemAuthTestDatabase shares the complete fixture with external provider
// tests. It exists only in the test binary, not the production auth API.
func NewBloemAuthTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	scratchDSN, cleanup, err := prepareAuthTestDatabase(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("drop auth fixture database: %v", err)
		}
	})
	pool, err := pgxpool.New(t.Context(), scratchDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestAuthScratchDatabaseIsolationAndCleanup(t *testing.T) {
	parent := NewBloemAuthTestDatabase(t)
	ctx := t.Context()
	parentDSN := parent.Config().ConnConfig.ConnString()
	var before string
	const snapshot = `SELECT json_build_object(
    'settings', (SELECT json_agg(s ORDER BY s.setrole) FROM pg_db_role_setting s
        WHERE s.setdatabase=(SELECT oid FROM pg_database WHERE datname=current_database())),
    'users', (SELECT count(*) FROM users),
    'history', (SELECT count(*) FROM goose_db_version))::text`
	if err := parent.QueryRow(ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	scratchDSN, cleanup, err := prepareAuthTestDatabase(ctx, parentDSN)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if err := cleanup(); err != nil {
				t.Error(err)
			}
		}
	})
	child, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(child.Close)
	var childName string
	if err := child.QueryRow(ctx, `SELECT current_database()`).Scan(&childName); err != nil {
		t.Fatal(err)
	}
	if childName == parent.Config().ConnConfig.Database {
		t.Fatal("scratch fixture reused its maintenance database")
	}
	if _, err := child.Exec(ctx, `CREATE TABLE public.auth_isolation_probe (id integer)`); err != nil {
		t.Fatal(err)
	}
	var leaked bool
	if err := parent.QueryRow(ctx, `SELECT to_regclass('public.auth_isolation_probe') IS NOT NULL`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("fixture leaked into maintenance database: %t, %v", leaked, err)
	}
	child.Close()
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	cleaned = true
	var exists bool
	if err := parent.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, childName).Scan(&exists); err != nil || exists {
		t.Fatalf("scratch database survived cleanup: %t, %v", exists, err)
	}
	var after string
	if err := parent.QueryRow(ctx, snapshot).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("maintenance database changed: before=%s after=%s", before, after)
	}
}

func prepareAuthTestDatabase(ctx context.Context, dsn string) (string, func() error, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return "", nil, errors.New("SILO_TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return "", nil, fmt.Errorf("connect maintenance database: %w", err)
	}
	name := "bloem_auth_suite_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		return "", nil, fmt.Errorf("create auth scratch database: %w", err)
	}
	cleanup := func() error {
		defer admin.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		return err
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = cleanup()
		}
	}()

	parsed.Path = "/" + name
	parsed.RawPath = ""
	// libpq URL parameters can override the path database. Keep the scratch
	// identity authoritative even if a caller supplied that redundant option.
	query := parsed.Query()
	query.Del("dbname")
	query.Set("search_path", "public")
	parsed.RawQuery = query.Encode()
	scratchDSN := parsed.String()
	pool, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		return "", nil, err
	}
	defer pool.Close()
	var connectedDatabase string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&connectedDatabase); err != nil {
		return "", nil, fmt.Errorf("connect auth scratch database: %w", err)
	}
	if connectedDatabase != name {
		return "", nil, errors.New("auth scratch connection selected a different database")
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		return "", nil, fmt.Errorf("migrate auth scratch database: %w", err)
	}
	if err := markTestDatabaseAsPolicyWriter(ctx, pool, scratchDSN); err != nil {
		return "", nil, fmt.Errorf("mark auth scratch policy writer: %w", err)
	}
	// ALTER DATABASE settings apply to new sessions, not migration connections.
	pool.Reset()
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		return "", nil, fmt.Errorf("finalize auth scratch policy authority: %w", err)
	}
	prepared = true
	return scratchDSN, cleanup, nil
}
