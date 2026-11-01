package orgdomains

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/migrations"
)

// newMigratedTestPool is a migrated, disposable database for one test.
func newMigratedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "bloem_orgdomains_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	testConfig.ConnConfig.Database = name
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return pool
}

func seedAccount(t *testing.T, pool *pgxpool.Pool, username string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', 'user', true) RETURNING id`, username, username+"@example.test").Scan(&id); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	return id
}

// seedOrganization creates an organization and makes each member's one
// membership point at it.
func seedOrganization(t *testing.T, pool *pgxpool.Pool, slug string, members ...int) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	owner := seedAccount(t, pool, slug+"-owner")
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, owner_account_id) VALUES ($1, $1, 'active', $2) RETURNING id`, slug, owner).Scan(&id); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_groups (organization_id, name, is_default) VALUES ($1, $2, true)`, id, slug+" default"); err != nil {
		t.Fatalf("seed access group: %v", err)
	}
	for _, m := range append([]int{owner}, members...) {
		joinOrganization(t, pool, id, m)
	}
	return id
}

func joinOrganization(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, account int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')
		ON CONFLICT (account_id) DO UPDATE SET organization_id = EXCLUDED.organization_id, status = 'active'`, org, account); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
}

func defaultOrganization(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT public.bloem_default_organization_id()`).Scan(&id); err != nil {
		t.Fatalf("default organization: %v", err)
	}
	return id
}

func TestAddDomainFirstIsPrimaryAndHostIsUnique(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	org2 := seedOrganization(t, pool, "main")
	org3 := seedOrganization(t, pool, "other")

	first, err := store.AddDomain(ctx, org2, "Main.Domain.com", SourcePlatform, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Host != "main.domain.com" || !first.Primary || first.OrganizationID != org2 || first.Source != SourcePlatform {
		t.Fatalf("first domain = %+v, want main.domain.com, primary, org2, platform", first)
	}
	if _, err := store.AddDomain(ctx, org3, "main.domain.com", SourceGarden, false); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("taken host: %v, want ErrDomainTaken", err)
	}
	if _, err := store.AddDomain(ctx, uuid.New(), "x.domain.com", SourcePlatform, false); !errors.Is(err, ErrOrganizationNotFound) {
		t.Fatalf("unknown organization: %v, want ErrOrganizationNotFound", err)
	}
	if _, err := store.AddDomain(ctx, org2, "bad host", SourcePlatform, false); !errors.Is(err, ErrInvalidHost) {
		t.Fatalf("invalid host: %v, want ErrInvalidHost", err)
	}
}

func TestSetPrimaryKeepsOnePrimary(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	org := seedOrganization(t, pool, "main")
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if _, err := store.AddDomain(ctx, org, host, SourcePlatform, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetPrimary(ctx, org, "b.example.com"); err != nil {
		t.Fatal(err)
	}
	domains, err := store.ListDomains(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 || domains[0].Host != "b.example.com" || !domains[0].Primary || domains[1].Primary {
		t.Fatalf("domains = %+v, want b primary first and a not primary", domains)
	}
	if err := store.SetPrimary(ctx, org, "c.example.com"); !errors.Is(err, ErrDomainNotFound) {
		t.Fatalf("unknown host: %v, want ErrDomainNotFound", err)
	}
	// Adding with primary=true moves the primary.
	if _, err := store.AddDomain(ctx, org, "c.example.com", SourcePlatform, true); err != nil {
		t.Fatal(err)
	}
	domains, _ = store.ListDomains(ctx, org)
	if domains[0].Host != "c.example.com" || !domains[0].Primary {
		t.Fatalf("after adding c as primary: %+v", domains)
	}
}

func TestRemovePrimaryPromotesOldest(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	org := seedOrganization(t, pool, "main")
	for _, host := range []string{"a.example.com", "b.example.com", "c.example.com"} {
		if _, err := store.AddDomain(ctx, org, host, SourcePlatform, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RemoveDomain(ctx, org, "a.example.com"); err != nil {
		t.Fatal(err)
	}
	domains, _ := store.ListDomains(ctx, org)
	if len(domains) != 2 || domains[0].Host != "b.example.com" || !domains[0].Primary {
		t.Fatalf("after removing the primary: %+v, want b promoted", domains)
	}
	if err := store.RemoveDomain(ctx, org, "a.example.com"); !errors.Is(err, ErrDomainNotFound) {
		t.Fatalf("removing twice: %v, want ErrDomainNotFound", err)
	}
	other := seedOrganization(t, pool, "other")
	if err := store.RemoveDomain(ctx, other, "b.example.com"); !errors.Is(err, ErrDomainNotFound) {
		t.Fatalf("removing another organization's host: %v, want ErrDomainNotFound", err)
	}
}

func TestBrandingValueEmptyDeletes(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	org := seedOrganization(t, pool, "main")
	if err := store.SetBrandingValue(ctx, org, "branding.server_name", "Main"); err != nil {
		t.Fatal(err)
	}
	values, err := store.BrandingValues(ctx, org)
	if err != nil || values["branding.server_name"] != "Main" {
		t.Fatalf("values = %v, %v", values, err)
	}
	if err := store.SetBrandingValue(ctx, org, "branding.server_name", ""); err != nil {
		t.Fatal(err)
	}
	if values, _ := store.BrandingValues(ctx, org); len(values) != 0 {
		t.Fatalf("after clearing: %v, want none", values)
	}
	if err := store.SetBrandingValue(ctx, org, "server.public_url", "https://x.example.com"); !errors.Is(err, ErrInvalidBrandingKey) {
		t.Fatalf("non-branding key: %v, want ErrInvalidBrandingKey", err)
	}
}

func TestAccountOrganization(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	member := seedAccount(t, pool, "member")
	org := seedOrganization(t, pool, "main", member)
	got, ok, err := store.AccountOrganization(ctx, member)
	if err != nil || !ok || got != org {
		t.Fatalf("AccountOrganization(member) = %v, %v, %v; want %v", got, ok, err, org)
	}
	if _, ok, err := store.AccountOrganization(ctx, 999999); err != nil || ok {
		t.Fatalf("unknown account: ok=%v err=%v, want not found", ok, err)
	}
}

func TestLoadSnapshot(t *testing.T) {
	pool := newMigratedTestPool(t)
	ctx := context.Background()
	store := NewStore(pool)
	org := seedOrganization(t, pool, "main")
	if _, err := store.AddDomain(ctx, org, "main.domain.com", SourcePlatform, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBrandingValue(ctx, org, "branding.accent_color", "#336699"); err != nil {
		t.Fatal(err)
	}
	snap, err := store.loadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.defaultOrg != defaultOrganization(t, pool) || snap.hosts["main.domain.com"] != org ||
		snap.primary[org] != "main.domain.com" || snap.branding[org]["branding.accent_color"] != "#336699" {
		t.Fatalf("snapshot = %+v", snap)
	}
}
