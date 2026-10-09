package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const (
	beforeProfileLoginCredentialsVersion = 20260813195641
	profileLoginCredentialsVersion       = 20260813200000
	loginSessionDeviceVersion            = 20261006162336
)

// TestProfileLoginCredentialsOnSiloOriginDatabase covers a database that ran
// Silo before Bloem. There the upstream login-session device migration already
// added a nullable auth_sessions.device_id, and Bloem's earlier-versioned
// profile credential migration then runs out of order. It has to converge the
// existing column instead of failing to add it.
func TestProfileLoginCredentialsOnSiloOriginDatabase(t *testing.T) {
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
	name := fmt.Sprintf("profile_login_credentials_test_%d", time.Now().UnixNano())
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
	if _, err := provider.UpTo(t.Context(), beforeProfileLoginCredentialsVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users (id, username, password_hash, role, enabled) VALUES (1, 'silo-user', 'x', 'user', true)`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ApplyVersion(t.Context(), loginSessionDeviceVersion, true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_sessions (id, user_id, expires_at) VALUES ('silo-session', 1, now() + interval '1 day')`); err != nil {
		t.Fatal(err)
	}

	if _, err := provider.UpTo(t.Context(), profileLoginCredentialsVersion); err != nil {
		t.Fatalf("profile login credentials on a Silo-origin database: %v", err)
	}

	var nullable, columnDefault, deviceID string
	if err := db.QueryRowContext(t.Context(), `
		SELECT is_nullable, column_default FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'auth_sessions' AND column_name = 'device_id'`,
	).Scan(&nullable, &columnDefault); err != nil {
		t.Fatal(err)
	}
	if nullable != "NO" || columnDefault != "''::text" {
		t.Fatalf("device_id is nullable=%s default=%s, want NOT NULL DEFAULT ''", nullable, columnDefault)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT device_id FROM auth_sessions WHERE id = 'silo-session'`).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	if deviceID != "" {
		t.Fatalf("existing session device_id = %q, want empty", deviceID)
	}

	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("remaining migrations after the out-of-order credential migration: %v", err)
	}
}
