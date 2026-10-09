package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// newMigratedTestDB returns a disposable database migrated to the latest
// version, the way a Bloem binary leaves one.
func newMigratedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("migrated_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	scoped := config.Copy()
	scoped.Database = name
	db := stdlib.OpenDB(*scoped)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"), goose.WithAllowOutofOrder(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestAuthSessionDeviceIDAcceptsUpstreamNull covers a Silo binary signing in on
// a Bloem-migrated database: upstream stores NULLIF(device, '') in device_id,
// which is NULL for a client that sends no device headers.
func TestAuthSessionDeviceIDAcceptsUpstreamNull(t *testing.T) {
	db := newMigratedTestDB(t)
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO users (id, username, password_hash, role, enabled) VALUES (1, 'silo-user', 'x', 'user', true)`)
	// The upstream statement shape (internal/auth/session.go in Silo).
	exec(`INSERT INTO auth_sessions
		(id, user_id, device_name, ip_address, expires_at, impersonator_user_id, impersonation_started_at, identity_id, provider_since, device_id, device_platform)
		VALUES ('s1', 1, 'web', NULL, now() + interval '1 day', NULL, NULL, NULL, NULL, NULLIF('', ''), NULLIF('', ''))`)
	var deviceID string
	if err := db.QueryRowContext(t.Context(), `SELECT device_id FROM auth_sessions WHERE id = 's1'`).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	if deviceID != "" {
		t.Fatalf("device_id = %q, want empty", deviceID)
	}
}
