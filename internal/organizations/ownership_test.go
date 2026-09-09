package organizations_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each case owns a fresh database; no migration or fixture touches the caller's database.
func ownershipDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := t.Context()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("organization tests require disposable loopback PostgreSQL")
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	name := fmt.Sprintf("silo_org_test_%d", time.Now().UnixNano())
	ident := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP DATABASE "+ident+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestOwnershipMigrationPreservesAccountsAndProfiles(t *testing.T) {
	pool := ownershipDatabase(t)
	ctx := t.Context()
	baseline := fstest.MapFS{}
	entries, err := fs.ReadDir(migrations.FS, "sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version, err := strconv.ParseInt(entry.Name()[:strings.IndexByte(entry.Name(), '_')], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if version >= 20260909224803 {
			continue
		}
		name := "sql/" + entry.Name()
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		baseline[name] = &fstest.MapFile{Data: body}
	}
	if err := database.RunMigrations(ctx, pool, baseline, "sql"); err != nil {
		t.Fatal(err)
	}
	originalIDs := map[string]int{}
	for _, role := range []string{"user", "admin"} {
		var id int
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, username, password_hash, role) VALUES ($1, $2, 'fixture', $3) RETURNING id`, role+"@example.test", role, role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		originalIDs[role] = id
		if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (id, user_id, name) VALUES ($1, $2, 'Original profile')`, "profile-"+role, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ role, slug string }{{"user", "default"}, {"admin", "platform"}} {
		var slug, role, profile, orgRole string
		var userID int
		if err := pool.QueryRow(ctx, `SELECT o.slug, u.role, p.id, u.organization_role, u.id FROM users u JOIN organizations o ON o.id=u.organization_id JOIN user_profiles p ON p.user_id=u.id WHERE u.username=$1`, tc.role).Scan(&slug, &role, &profile, &orgRole, &userID); err != nil {
			t.Fatal(err)
		}
		if userID != originalIDs[tc.role] || slug != tc.slug || role != tc.role || profile != "profile-"+tc.role || orgRole != "member" {
			t.Fatalf("migrated identity = %q %q %q %q", slug, role, profile, orgRole)
		}
	}
	for _, query := range []string{
		`UPDATE users SET organization_id=NULL WHERE username='user'`,
		`UPDATE users SET organization_id=-1 WHERE username='user'`,
		`UPDATE users SET organization_role='owner' WHERE username='user'`,
		`DELETE FROM organizations WHERE slug='default'`,
	} {
		if _, err := pool.Exec(ctx, query); err == nil {
			t.Fatalf("invalid ownership accepted: %s", query)
		}
	}
}
