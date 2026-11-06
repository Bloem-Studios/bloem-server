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
// version, the way a Bloem binary leaves one, and its migration provider.
func newMigratedTestDB(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	db, provider := newTestDBMigratedTo(t, 0)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, provider
}

// newTestDBMigratedTo returns a disposable database migrated up to version, or
// not migrated at all when version is 0.
func newTestDBMigratedTo(t *testing.T, version int64) (*sql.DB, *goose.Provider) {
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
	if version > 0 {
		if _, err := provider.UpTo(t.Context(), version); err != nil {
			t.Fatal(err)
		}
	}
	return db, provider
}

// beforeAuthSessionDeviceIDNull is the newest migration that still stores a
// session with no device as ”.
const beforeAuthSessionDeviceIDNull = 20261106000051

// TestAuthSessionDeviceIDKeepsUpstreamNull covers a Silo binary signing in on
// a Bloem-migrated database: upstream stores NULLIF(device, ”) in device_id,
// which is NULL for a client that sends no device headers, and its queries read
// NULL as "this login names no device". Bloem keeps Silo's NULL.
func TestAuthSessionDeviceIDKeepsUpstreamNull(t *testing.T) {
	db, _ := newMigratedTestDB(t)
	exec(t, db,
		`INSERT INTO users (id, username, password_hash, role, enabled) VALUES (1, 'silo-user', 'x', 'user', true)`,
		// The upstream statement shape (internal/auth/session.go in Silo).
		`INSERT INTO auth_sessions
		(id, user_id, device_name, ip_address, expires_at, impersonator_user_id, impersonation_started_at, identity_id, provider_since, device_id, device_platform)
		VALUES ('s1', 1, 'web', NULL, now() + interval '1 day', NULL, NULL, NULL, NULL, NULLIF('', ''), NULLIF('', ''))`,
	)
	assertSessionDeviceID(t, db, "s1", sql.NullString{})
}

// TestAuthSessionDeviceIDNullConvertsBlankRows covers a database that stored
// Bloem's ” for a session with no device: the migration turns those rows into
// Silo's NULL and leaves a reported device alone.
func TestAuthSessionDeviceIDNullConvertsBlankRows(t *testing.T) {
	db, provider := newTestDBMigratedTo(t, beforeAuthSessionDeviceIDNull)
	exec(t, db,
		`INSERT INTO users (id, username, password_hash, role, enabled) VALUES (1, 'bloem-user', 'x', 'user', true)`,
		`INSERT INTO auth_sessions (id, user_id, device_name, device_id, expires_at) VALUES
		('blank', 1, 'web', '', now() + interval '1 day'),
		('phone', 1, 'phone', 'phone-1', now() + interval '1 day')`,
	)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertSessionDeviceID(t, db, "blank", sql.NullString{})
	assertSessionDeviceID(t, db, "phone", sql.NullString{String: "phone-1", Valid: true})
}

func assertSessionDeviceID(t *testing.T, db *sql.DB, id string, want sql.NullString) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRowContext(t.Context(), `SELECT device_id FROM auth_sessions WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("session %s device_id = %+v, want %+v", id, got, want)
	}
}
