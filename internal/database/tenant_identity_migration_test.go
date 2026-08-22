package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/userstore/pgstore"
	"github.com/Silo-Server/silo-server/migrations"
)

const (
	tenantIdentityPreviousMigration = 20260812163547
	tenantIdentityMigration         = 20260812190000
)

func TestTenantIdentityMigrationPreviousVersionIsImmediatePredecessor(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "sql/*.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}

	var immediatePredecessor int64
	for _, file := range files {
		versionText, _, ok := strings.Cut(path.Base(file), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			continue
		}
		if version < tenantIdentityMigration && version > immediatePredecessor {
			immediatePredecessor = version
		}
	}
	if tenantIdentityPreviousMigration != immediatePredecessor {
		t.Fatalf("tenant identity predecessor = %d, want immediate embedded predecessor %d", tenantIdentityPreviousMigration, immediatePredecessor)
	}
}

func TestTenantIdentityMigrationRunbookUsesImmediateRollbackTarget(t *testing.T) {
	runbook, err := os.ReadFile("../../docs/architecture/v2-security-foundation.md")
	if err != nil {
		t.Fatalf("read tenant security runbook: %v", err)
	}
	want := fmt.Sprintf("make migrate-down-to VERSION=%d", tenantIdentityPreviousMigration)
	if !strings.Contains(string(runbook), want) {
		t.Fatalf("tenant security runbook does not contain exact rollback command %q", want)
	}
}

func TestTenantIdentityMigrationBackfill(t *testing.T) {
	dsn := requiredPostgresTestDatabaseURL(t)

	tests := []struct {
		name                   string
		adminCount             int
		ordinaryUserCount      int
		disabledAdminCount     int
		wantOwner              bool
		wantResolutionRequired bool
	}{
		{name: "fresh install", adminCount: 0},
		{name: "single setup admin", adminCount: 1, wantOwner: true},
		{name: "ambiguous admins", adminCount: 2, wantResolutionRequired: true},
		{name: "ordinary users plus one admin", adminCount: 1, ordinaryUserCount: 2, wantOwner: true},
		{name: "disabled admins do not make ownership ambiguous", adminCount: 1, disabledAdminCount: 1, wantOwner: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)

			// Starting at the migration immediately before this boundary models a
			// real upgrade rather than testing the final schema in isolation.
			if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
				t.Fatalf("migrate initial schema: %v", err)
			}
			if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", tenantIdentityPreviousMigration); err != nil {
				t.Fatalf("migrate to pre-tenant-identity version: %v", err)
			}

			seed := seedTenantIdentityLegacyState(ctx, t, pool, tt.adminCount, tt.ordinaryUserCount, tt.disabledAdminCount)
			before := snapshotTenantIdentityLegacyState(ctx, t, pool, false)

			if err := migrateTenantIdentityBoundaryUp(ctx, pool); err != nil {
				t.Fatalf("migrate tenant identity foundation: %v", err)
			}
			assertTenantIdentityUpgrade(ctx, t, pool, seed, before, tt.wantOwner, tt.wantResolutionRequired)

			if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", tenantIdentityPreviousMigration); err != nil {
				t.Fatalf("migrate tenant identity foundation down: %v", err)
			}
			assertTenantIdentityBoundaryRemoved(ctx, t, pool)
			if got := snapshotTenantIdentityLegacyState(ctx, t, pool, false); got != before {
				t.Errorf("down migration changed legacy identity/access-group state:\n got %s\nwant %s", got, before)
			}

			if err := migrateTenantIdentityBoundaryUp(ctx, pool); err != nil {
				t.Fatalf("re-migrate tenant identity foundation: %v", err)
			}
			assertTenantIdentityUpgrade(ctx, t, pool, seed, before, tt.wantOwner, tt.wantResolutionRequired)
		})
	}
}

// TestTenantIdentityMigrationSupportsLegacyWritePaths exercises the v1 stores
// directly. They intentionally do not carry tenant identity yet, so this
// schema boundary must provide the default organization without weakening its
// NOT NULL guarantees.
func TestTenantIdentityMigrationSupportsLegacyWritePaths(t *testing.T) {
	dsn := requiredPostgresTestDatabaseURL(t)

	t.Run("profile creation", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)

		var userID int
		var legacyAccessGroupID int64
		if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username, email, password_hash, role, access_group_id)
VALUES ('tenant-write-profile', 'tenant-write-profile@example.com', 'x', 'user',
        (SELECT id FROM public.access_groups WHERE is_default))
RETURNING id, access_group_id`).Scan(&userID, &legacyAccessGroupID); err != nil {
			t.Fatalf("seed profile owner: %v", err)
		}
		if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(ctx, userID, "user"); err != nil {
			t.Fatalf("provision profile owner membership: %v", err)
		}
		store, err := pgstore.NewPostgresProvider(pool).ForUser(ctx, userID)
		if err != nil {
			t.Fatalf("create postgres user store: %v", err)
		}
		if err := store.CreateProfile(ctx, userstore.Profile{ID: "v1-profile", Name: "V1 Profile"}); err != nil {
			t.Fatalf("legacy profile create: %v", err)
		}

		var profileOrganizationID string
		var profileAccessGroupID int64
		if err := pool.QueryRow(ctx, `
SELECT organization_id::text, access_group_id
FROM public.user_profiles
WHERE user_id = $1 AND id = 'v1-profile'`, userID).Scan(&profileOrganizationID, &profileAccessGroupID); err != nil {
			t.Fatalf("read profile tenant identity: %v", err)
		}
		assertDefaultOrganizationID(t, ctx, pool, profileOrganizationID)
		if profileAccessGroupID != legacyAccessGroupID {
			t.Errorf("profile access group = %d, want copy of user legacy access group %d", profileAccessGroupID, legacyAccessGroupID)
		}
	})

	t.Run("access group creation", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)
		var defaultOrganizationID uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM public.organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
			t.Fatalf("load default organization: %v", err)
		}

		group, err := access.NewGroupStore(pool).Create(ctx, defaultOrganizationID, access.CreateGroupInput{
			Name:            "V1 access group",
			Description:     "created by the legacy store",
			DownloadAllowed: true,
			RequestsAllowed: true,
		})
		if err != nil {
			t.Fatalf("legacy access group create: %v", err)
		}

		var organizationID string
		if err := pool.QueryRow(ctx, `SELECT organization_id::text FROM public.access_groups WHERE id = $1`, group.ID).Scan(&organizationID); err != nil {
			t.Fatalf("read access group organization: %v", err)
		}
		assertDefaultOrganizationID(t, ctx, pool, organizationID)
	})

	t.Run("profile insert without organization is rejected", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)

		var userID int
		if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username, email, password_hash, role)
VALUES ('tenant-malformed-profile', 'tenant-malformed-profile@example.com', 'x', 'user')
RETURNING id`).Scan(&userID); err != nil {
			t.Fatalf("seed malformed profile owner: %v", err)
		}
		_, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (user_id, id, name)
VALUES ($1, 'missing-organization', 'Malformed')`, userID)
		assertTenantIdentityNotNullViolation(t, err, "profile insert without organization")
	})

	t.Run("legacy access group insert defaults to the default organization", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)

		var organizationID string
		if err := pool.QueryRow(ctx, `
INSERT INTO public.access_groups (name)
VALUES ('Legacy access group')
RETURNING organization_id::text`).Scan(&organizationID); err != nil {
			t.Fatalf("legacy access group insert: %v", err)
		}
		assertDefaultOrganizationID(t, ctx, pool, organizationID)
	})

	t.Run("cross organization profile group pairing is rejected", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)

		var userID int
		if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username, email, password_hash, role)
VALUES ('tenant-cross-org-profile', 'tenant-cross-org-profile@example.com', 'x', 'user')
RETURNING id`).Scan(&userID); err != nil {
			t.Fatalf("seed cross organization profile owner: %v", err)
		}
		var defaultOrganizationID, otherOrganizationID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM public.organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
			t.Fatalf("read default organization: %v", err)
		}
		if err := pool.QueryRow(ctx, `
INSERT INTO public.organizations (slug, name, status)
VALUES ('other-organization', 'Other Organization', 'initializing')
RETURNING id::text`).Scan(&otherOrganizationID); err != nil {
			t.Fatalf("create second organization: %v", err)
		}
		var otherGroupID int64
		if err := pool.QueryRow(ctx, `
INSERT INTO public.access_groups (organization_id, name)
VALUES ($1, 'Other organization group')
RETURNING id`, otherOrganizationID).Scan(&otherGroupID); err != nil {
			t.Fatalf("create second organization group: %v", err)
		}
		_, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (organization_id, access_group_id, user_id, id, name)
VALUES ($1, $2, $3, 'cross-organization', 'Cross Organization')`,
			defaultOrganizationID, otherGroupID, userID)
		if err == nil {
			t.Fatal("profile accepted an access group from another organization")
		}
	})

	t.Run("active organization without owner is rejected", func(t *testing.T) {
		ctx := context.Background()
		pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
		migrateTenantIdentityLatest(t, ctx, pool)

		_, err := pool.Exec(ctx, `
INSERT INTO public.organizations (slug, name, status)
VALUES ('ownerless-active', 'Ownerless Active', 'active')`)
		if err == nil {
			t.Fatal("active organization without owner was accepted")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("active organization without owner error = %v, want SQLSTATE 23514", err)
		}
	})
}

func migrateTenantIdentityLatest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest schema: %v", err)
	}
}

func assertDefaultOrganizationID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, got string) {
	t.Helper()
	var want string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.organizations WHERE is_default`).Scan(&want); err != nil {
		t.Fatalf("read default organization: %v", err)
	}
	if got != want {
		t.Errorf("organization = %q, want default organization %q", got, want)
	}
}

func assertTenantIdentityNotNullViolation(t *testing.T, err error, operation string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded, want not-null violation", operation)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23502" {
		t.Fatalf("%s error = %v, want SQLSTATE 23502", operation, err)
	}
}

type tenantIdentitySeed struct {
	users []tenantIdentitySeedUser
}

type tenantIdentitySeedUser struct {
	id            int
	role          string
	accessGroupID sql.NullInt64
}

func seedTenantIdentityLegacyState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, adminCount, ordinaryUserCount, disabledAdminCount int) tenantIdentitySeed {
	t.Helper()

	var groupOneID, groupTwoID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO public.access_groups (name, description)
VALUES ('Tenant migration group one', 'first legacy group')
RETURNING id`).Scan(&groupOneID); err != nil {
		t.Fatalf("seed first access group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO public.access_groups (name, description)
VALUES ('Tenant migration group two', 'second legacy group')
RETURNING id`).Scan(&groupTwoID); err != nil {
		t.Fatalf("seed second access group: %v", err)
	}

	seed := tenantIdentitySeed{}
	insertUser := func(role string, enabled bool, accessGroupID int64, ordinal int) {
		t.Helper()
		var id int
		username := fmt.Sprintf("tenant-identity-%s-%t-%d", role, enabled, ordinal)
		if err := pool.QueryRow(ctx, `
INSERT INTO public.users (username, email, password_hash, role, enabled, access_group_id)
VALUES ($1, $2, 'x', $3, $4, $5)
RETURNING id`, username, username+"@example.com", role, enabled, accessGroupID).Scan(&id); err != nil {
			t.Fatalf("seed %s user: %v", role, err)
		}
		for _, profileID := range []string{"first", "second"} {
			if _, err := pool.Exec(ctx, `
INSERT INTO public.user_profiles (user_id, id, name)
VALUES ($1, $2, $3)`, id, profileID, username+" "+profileID); err != nil {
				t.Fatalf("seed %s profile: %v", role, err)
			}
		}
		seed.users = append(seed.users, tenantIdentitySeedUser{
			id: id, role: role, accessGroupID: sql.NullInt64{Int64: accessGroupID, Valid: true},
		})
	}

	for i := 0; i < adminCount; i++ {
		insertUser("admin", true, groupOneID, i)
	}
	for i := 0; i < ordinaryUserCount; i++ {
		insertUser("user", true, groupTwoID, i)
	}
	for i := 0; i < disabledAdminCount; i++ {
		insertUser("admin", false, groupTwoID, i)
	}
	return seed
}

func assertTenantIdentityUpgrade(ctx context.Context, t *testing.T, pool *pgxpool.Pool, seed tenantIdentitySeed, legacySnapshot string, wantOwner, wantResolutionRequired bool) {
	t.Helper()

	var organizationCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.organizations`).Scan(&organizationCount); err != nil {
		t.Fatalf("count organizations: %v", err)
	}
	if organizationCount != 1 {
		t.Errorf("organizations = %d, want exactly one default organization", organizationCount)
	}

	var organizationID string
	var organizationStatus string
	var organizationOwner sql.NullInt64
	if err := pool.QueryRow(ctx, `
SELECT id::text, status, owner_account_id
FROM public.organizations
WHERE is_default`).Scan(&organizationID, &organizationStatus, &organizationOwner); err != nil {
		t.Fatalf("read default organization: %v", err)
	}
	wantStatus := "initializing"
	if wantOwner {
		wantStatus = "active"
	}
	if organizationStatus != wantStatus {
		t.Errorf("organization status = %q, want %q", organizationStatus, wantStatus)
	}

	var securityOwner sql.NullInt64
	var resolutionRequired bool
	if err := pool.QueryRow(ctx, `
SELECT owner_account_id, ownership_resolution_required
FROM public.platform_security
WHERE singleton`).Scan(&securityOwner, &resolutionRequired); err != nil {
		t.Fatalf("read platform security: %v", err)
	}
	if resolutionRequired != wantResolutionRequired {
		t.Errorf("ownership_resolution_required = %t, want %t", resolutionRequired, wantResolutionRequired)
	}
	if organizationOwner.Valid != wantOwner || securityOwner.Valid != wantOwner {
		t.Errorf("owner presence = organization:%t platform:%t, want %t", organizationOwner.Valid, securityOwner.Valid, wantOwner)
	}
	if wantOwner {
		var expectedOwner int
		for _, user := range seed.users {
			if user.role == "admin" {
				expectedOwner = user.id
				break
			}
		}
		if organizationOwner.Int64 != int64(expectedOwner) || securityOwner.Int64 != int64(expectedOwner) {
			t.Errorf("owners = organization:%d platform:%d, want %d", organizationOwner.Int64, securityOwner.Int64, expectedOwner)
		}
	}

	var membershipCount int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*) FROM public.organization_memberships WHERE organization_id = $1`, organizationID).Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if membershipCount != len(seed.users) {
		t.Errorf("memberships = %d, want one for each of %d seeded users", membershipCount, len(seed.users))
	}
	for _, user := range seed.users {
		var legacyRole, status string
		if err := pool.QueryRow(ctx, `
SELECT legacy_role, status
FROM public.organization_memberships
WHERE organization_id = $1 AND account_id = $2`, organizationID, user.id).Scan(&legacyRole, &status); err != nil {
			t.Errorf("read membership for user %d: %v", user.id, err)
			continue
		}
		if legacyRole != user.role || status != "active" {
			t.Errorf("membership for user %d = (%q, %q), want (%q, %q)", user.id, legacyRole, status, user.role, "active")
		}
	}

	var groupCount, scopedGroupCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.access_groups`).Scan(&groupCount); err != nil {
		t.Fatalf("count access groups: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM public.access_groups WHERE organization_id = $1`, organizationID).Scan(&scopedGroupCount); err != nil {
		t.Fatalf("count organization access groups: %v", err)
	}
	if scopedGroupCount != groupCount {
		t.Errorf("organization-scoped access groups = %d, want all %d legacy groups", scopedGroupCount, groupCount)
	}

	for _, user := range seed.users {
		rows, err := pool.Query(ctx, `
SELECT organization_id::text, access_group_id
FROM public.user_profiles
WHERE user_id = $1`, user.id)
		if err != nil {
			t.Fatalf("read profiles for user %d: %v", user.id, err)
		}
		profileCount := 0
		for rows.Next() {
			profileCount++
			var profileOrganizationID string
			var profileAccessGroupID sql.NullInt64
			if err := rows.Scan(&profileOrganizationID, &profileAccessGroupID); err != nil {
				rows.Close()
				t.Fatalf("scan profile for user %d: %v", user.id, err)
			}
			if profileOrganizationID != organizationID {
				t.Errorf("profile organization = %q, want %q", profileOrganizationID, organizationID)
			}
			if profileAccessGroupID != user.accessGroupID {
				t.Errorf("profile access group = %+v, want copy of users.access_group_id %+v", profileAccessGroupID, user.accessGroupID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate profiles for user %d: %v", user.id, err)
		}
		rows.Close()
		if profileCount != 2 {
			t.Errorf("profiles for user %d = %d, want 2", user.id, profileCount)
		}
	}

	if got := snapshotTenantIdentityLegacyState(ctx, t, pool, true); got != legacySnapshot {
		t.Errorf("up migration changed legacy identity/access-group state:\n got %s\nwant %s", got, legacySnapshot)
	}
}

func assertTenantIdentityBoundaryRemoved(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, table := range []string{"platform_security", "organizations", "organization_memberships"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s after down migration: %v", table, err)
		}
		if exists {
			t.Errorf("down migration left %s behind", table)
		}
	}
	for _, column := range []struct{ table, column string }{
		{"user_profiles", "organization_id"},
		{"user_profiles", "access_group_id"},
		{"access_groups", "organization_id"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
)`, column.table, column.column).Scan(&exists); err != nil {
			t.Fatalf("check %s.%s after down migration: %v", column.table, column.column, err)
		}
		if exists {
			t.Errorf("down migration left %s.%s behind", column.table, column.column)
		}
	}
	var functionExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regprocedure('public.vondel_default_organization_id()') IS NOT NULL`).Scan(&functionExists); err != nil {
		t.Fatalf("check default organization function after down migration: %v", err)
	}
	if functionExists {
		t.Error("down migration left vondel_default_organization_id() behind")
	}
}

func snapshotTenantIdentityLegacyState(ctx context.Context, t *testing.T, pool *pgxpool.Pool, removeTenantColumns bool) string {
	t.Helper()
	// Columns that later migrations add to user_profiles are not part of the
	// legacy identity state this snapshot pins. Subtracting a key that does
	// not exist yet is a no-op, so the same projection works on both sides of
	// the boundary.
	profiles := "to_jsonb(p) - 'login_email' - 'password_hash' - 'credential_revision'"
	groups := "to_jsonb(g)"
	if removeTenantColumns {
		profiles += " - 'organization_id' - 'access_group_id'"
		groups += " - 'organization_id'"
	}
	query := fmt.Sprintf(`
SELECT jsonb_build_object(
    'users', COALESCE((SELECT jsonb_agg(to_jsonb(u) ORDER BY u.id) FROM public.users u), '[]'::jsonb),
    'profiles', COALESCE((SELECT jsonb_agg(%s ORDER BY p.user_id, p.id) FROM public.user_profiles p), '[]'::jsonb),
    'access_groups', COALESCE((SELECT jsonb_agg(%s ORDER BY g.id) FROM public.access_groups g), '[]'::jsonb)
)::text`, profiles, groups)
	var snapshot string
	if err := pool.QueryRow(ctx, query).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot legacy state: %v", err)
	}
	return snapshot
}

func migrateTenantIdentityBoundaryUp(ctx context.Context, pool *pgxpool.Pool) error {
	provider, err := newMigrationProvider(pool, migrations.FS, "sql")
	if err != nil {
		return err
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.UpTo(ctx, tenantIdentityMigration); err != nil {
		return fmt.Errorf("migrate to tenant identity boundary: %w", err)
	}
	return nil
}

func newTenantIdentityDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_tenant_identity_" + hex.EncodeToString(random[:])

	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database %q: %v", name, err)
	}

	testConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse disposable database URL: %v", err)
	}
	testConfig.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		terminateCtx, cancelTerminate := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = admin.Exec(terminateCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		cancelTerminate()

		// Cleanup can queue behind other disposable-database migrations in the
		// full suite. Do not let that wait consume the DROP operation's budget.
		dropCtx, cancelDrop := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelDrop()
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	return pool
}

func newDisposableMigrationDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return newTenantIdentityDisposableDatabase(t, context.Background(), requiredPostgresTestDatabaseURL(t))
}

func requiredPostgresTestDatabaseURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	return dsn
}
