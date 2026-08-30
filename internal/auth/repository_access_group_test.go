package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestUserRepositoryUpdateAccessGroupIDDB(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	groupID := insertAuthAccessGroupTestGroup(t, ctx, pool, suffix)
	userID := insertAuthAccessGroupTestUser(t, ctx, pool, suffix)
	users := NewUserRepository(pool)

	before, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID() before update error: %v", err)
	}

	if err := users.Update(ctx, userID, models.UpdateUserInput{
		AccessGroupID: models.SetValue(groupID),
	}); err != nil {
		t.Fatalf("Update(access_group_id) error: %v", err)
	}
	user, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.AccessGroupID == nil || *user.AccessGroupID != groupID {
		t.Fatalf("AccessGroupID = %#v, want %d", user.AccessGroupID, groupID)
	}
	if user.AccessPolicyRevision != before.AccessPolicyRevision+1 {
		t.Fatalf("AccessPolicyRevision = %d after group change, want %d",
			user.AccessPolicyRevision, before.AccessPolicyRevision+1)
	}

	// Re-asserting the same group is a no-op for the policy revision.
	if err := users.Update(ctx, userID, models.UpdateUserInput{
		AccessGroupID: models.SetValue(groupID),
	}); err != nil {
		t.Fatalf("Update(same access_group_id) error: %v", err)
	}
	unchanged, err := users.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID() after same-group update error: %v", err)
	}
	if unchanged.AccessPolicyRevision != user.AccessPolicyRevision {
		t.Fatalf("AccessPolicyRevision = %d after same-group update, want unchanged %d",
			unchanged.AccessPolicyRevision, user.AccessPolicyRevision)
	}

	if err := users.Update(ctx, userID, models.UpdateUserInput{AccessGroupID: models.ClearValue[int64]()}); err != nil {
		t.Fatalf("Update(access_group_id null) error: %v", err)
	}
	user, err = users.GetByID(ctx, userID)
	if err != nil {
		t.Fatalf("GetByID() after null error: %v", err)
	}
	if user.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v, want nil", user.AccessGroupID)
	}
	if user.AccessPolicyRevision != unchanged.AccessPolicyRevision+1 {
		t.Fatalf("AccessPolicyRevision = %d after ungrouping, want %d",
			user.AccessPolicyRevision, unchanged.AccessPolicyRevision+1)
	}
}

func TestUserRepositoryUpdatePromotingToAdminClearsAccessGroupDB(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	groupID := insertAuthAccessGroupTestGroup(t, ctx, pool, suffix)
	users := NewUserRepository(pool)
	created, err := users.Create(ctx, createAuthAccessGroupUserInput(suffix, "promote", &groupID))
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if created.AccessGroupID == nil || *created.AccessGroupID != groupID {
		t.Fatalf("AccessGroupID = %#v, want %d", created.AccessGroupID, groupID)
	}

	role := "admin"
	if err := users.Update(ctx, created.ID, models.UpdateUserInput{Role: &role}); err != nil {
		t.Fatalf("Update(role=admin) error: %v", err)
	}
	user, err := users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("Role = %q, want admin", user.Role)
	}
	if user.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v after promote, want nil", user.AccessGroupID)
	}

	// A group written on its own is resolved against the row's role in the
	// same statement, so a write that raced a promotion cannot group the
	// admin; the handler's 422 is a preflight, not the invariant.
	if err := users.Update(ctx, created.ID, models.UpdateUserInput{AccessGroupID: models.SetValue(groupID)}); err != nil {
		t.Fatalf("Update(group on admin) error: %v", err)
	}
	user, err = users.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v after grouping an admin, want nil", user.AccessGroupID)
	}
	if user.AccessPolicyRevision != created.AccessPolicyRevision+1 {
		t.Fatalf("AccessPolicyRevision = %d, want %d (promote bumped once, no-op group write must not)",
			user.AccessPolicyRevision, created.AccessPolicyRevision+1)
	}
}

// Demoting an admin without naming a group lands it on the default group (as
// create does) so it never becomes an uncapped non-admin; an explicit group in
// the same write wins, and re-asserting role=user on an ordinary account does
// not move it.
func TestUserRepositoryUpdateDemotingAdminAssignsDefaultAccessGroupDB(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	seedID := defaultAuthAccessGroupSeedID(t, ctx, pool)
	t.Cleanup(func() {
		restoreAuthDefaultAccessGroup(t, ctx, pool, seedID)
	})
	defaultID := insertAuthAccessGroupTestGroupWithLabel(t, ctx, pool, suffix, "default")
	setAuthDefaultAccessGroup(t, ctx, pool, defaultID)
	otherID := insertAuthAccessGroupTestGroupWithLabel(t, ctx, pool, suffix, "other")
	users := NewUserRepository(pool)

	adminInput := createAuthAccessGroupUserInput(suffix, "demote", nil)
	adminInput.Role = "admin"
	admin, err := users.Create(ctx, adminInput)
	if err != nil {
		t.Fatalf("Create(admin) error: %v", err)
	}

	roleUser := "user"
	if err := users.Update(ctx, admin.ID, models.UpdateUserInput{Role: &roleUser}); err != nil {
		t.Fatalf("Update(role=user) error: %v", err)
	}
	user, err := users.GetByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.AccessGroupID == nil || *user.AccessGroupID != defaultID {
		t.Fatalf("AccessGroupID = %#v after demote, want default group %d", user.AccessGroupID, defaultID)
	}
	if user.AccessPolicyRevision != admin.AccessPolicyRevision+1 {
		t.Fatalf("AccessPolicyRevision = %d after demote, want %d", user.AccessPolicyRevision, admin.AccessPolicyRevision+1)
	}

	// Re-asserting role=user on a grouped account is not a demotion and must
	// not move it off its group.
	if err := users.Update(ctx, admin.ID, models.UpdateUserInput{
		Role: &roleUser, AccessGroupID: models.SetValue(otherID),
	}); err != nil {
		t.Fatalf("Update(explicit group) error: %v", err)
	}
	if err := users.Update(ctx, admin.ID, models.UpdateUserInput{Role: &roleUser}); err != nil {
		t.Fatalf("Update(role=user again) error: %v", err)
	}
	user, err = users.GetByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.AccessGroupID == nil || *user.AccessGroupID != otherID {
		t.Fatalf("AccessGroupID = %#v after re-asserting role, want %d", user.AccessGroupID, otherID)
	}

	// Demoting with an explicit group honors it over the default.
	roleAdmin := "admin"
	if err := users.Update(ctx, admin.ID, models.UpdateUserInput{Role: &roleAdmin}); err != nil {
		t.Fatalf("Update(role=admin) error: %v", err)
	}
	if err := users.Update(ctx, admin.ID, models.UpdateUserInput{
		Role: &roleUser, AccessGroupID: models.SetValue(otherID),
	}); err != nil {
		t.Fatalf("Update(demote with group) error: %v", err)
	}
	user, err = users.GetByID(ctx, admin.ID)
	if err != nil {
		t.Fatalf("GetByID() error: %v", err)
	}
	if user.AccessGroupID == nil || *user.AccessGroupID != otherID {
		t.Fatalf("AccessGroupID = %#v after demote with group, want %d", user.AccessGroupID, otherID)
	}
}

func TestUserRepositoryCreateAssignsDefaultAccessGroupDB(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	seedID := defaultAuthAccessGroupSeedID(t, ctx, pool)
	t.Cleanup(func() {
		restoreAuthDefaultAccessGroup(t, ctx, pool, seedID)
	})
	users := NewUserRepository(pool)

	defaultID := insertAuthAccessGroupTestGroupWithLabel(t, ctx, pool, suffix, "default")
	setAuthDefaultAccessGroup(t, ctx, pool, defaultID)

	created, err := users.Create(ctx, createAuthAccessGroupUserInput(suffix, "assigned-default", nil))
	if err != nil {
		t.Fatalf("Create(default assignment) error: %v", err)
	}
	if created.AccessGroupID == nil || *created.AccessGroupID != defaultID {
		t.Fatalf("AccessGroupID = %#v, want default group %d", created.AccessGroupID, defaultID)
	}

	adminInput := createAuthAccessGroupUserInput(suffix, "admin", nil)
	adminInput.Role = "admin"
	created, err = users.Create(ctx, adminInput)
	if err != nil {
		t.Fatalf("Create(admin) error: %v", err)
	}
	if created.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v for admin, want nil (admins stay ungrouped)", created.AccessGroupID)
	}

	groupedAdminInput := createAuthAccessGroupUserInput(suffix, "grouped-admin", &defaultID)
	groupedAdminInput.Role = "admin"
	created, err = users.Create(ctx, groupedAdminInput)
	if err != nil {
		t.Fatalf("Create(admin with explicit group) error: %v", err)
	}
	if created.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v for admin with explicit group, want nil", created.AccessGroupID)
	}

	explicitID := insertAuthAccessGroupTestGroupWithLabel(t, ctx, pool, suffix, "explicit")
	created, err = users.Create(ctx, createAuthAccessGroupUserInput(suffix, "explicit", &explicitID))
	if err != nil {
		t.Fatalf("Create(explicit group) error: %v", err)
	}
	if created.AccessGroupID == nil || *created.AccessGroupID != explicitID {
		t.Fatalf("AccessGroupID = %#v, want explicit group %d", created.AccessGroupID, explicitID)
	}

	clearAuthDefaultAccessGroup(t, ctx, pool)
	created, err = users.Create(ctx, createAuthAccessGroupUserInput(suffix, "no-default", nil))
	if err != nil {
		t.Fatalf("Create(no default) error: %v", err)
	}
	if created.AccessGroupID != nil {
		t.Fatalf("AccessGroupID = %#v, want nil without a default group", created.AccessGroupID)
	}
}

func TestUserRepositoryCreateUsesDeploymentDefaultOrganizationGroupDB(t *testing.T) {
	ctx, pool, suffix := newAccessGroupUserRepoDBTest(t)
	var deploymentDefaultGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT g.id
		FROM access_groups g
		JOIN organizations o ON o.id = g.organization_id
		WHERE o.is_default
		  AND g.is_default`).Scan(&deploymentDefaultGroupID); err != nil {
		t.Fatalf("load deployment default organization group: %v", err)
	}

	foreignOrganizationID := uuid.New()
	foreignSlug := "auth-access-group-test-" + suffix
	foreignGroupName := "Auth Access Group Test " + suffix + " foreign default"
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, slug, name, status)
		VALUES ($1, $2, $3, 'initializing')`,
		foreignOrganizationID, foreignSlug, "Auth Access Group Test "+suffix); err != nil {
		t.Fatalf("insert foreign organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, $2, true)`, foreignOrganizationID, foreignGroupName); err != nil {
		t.Fatalf("insert foreign organization default group: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE organization_id = $1`, foreignOrganizationID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, foreignOrganizationID)
	})

	created, err := NewUserRepository(pool).Create(ctx, createAuthAccessGroupUserInput(suffix, "two-org-defaults", nil))
	if err != nil {
		t.Fatalf("Create(two organization defaults) error: %v", err)
	}
	if created.AccessGroupID == nil || *created.AccessGroupID != deploymentDefaultGroupID {
		t.Fatalf("AccessGroupID = %#v, want deployment default organization group %d", created.AccessGroupID, deploymentDefaultGroupID)
	}
}

func newAccessGroupUserRepoDBTest(t *testing.T) (context.Context, *pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newAuthAccessGroupDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database is in the compatibility phase, which freezes
	// every policy write. Hand the authority over so these tests exercise the
	// steady state the repository now targets.
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}

	var tableName *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.access_groups')::text`).Scan(&tableName); err != nil {
		t.Fatalf("check access_groups table: %v", err)
	}
	if tableName == nil || *tableName == "" {
		t.Skip("test database has not applied access groups migration")
	}
	if !authAccessGroupDefaultColumnExists(t, ctx, pool) {
		t.Skip("test database has not applied default access group migration")
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE username LIKE $1`, "auth-access-group-test-"+suffix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM access_groups WHERE name LIKE $1`, "Auth Access Group Test "+suffix+"%")
	})
	return ctx, pool, suffix
}

func newAuthAccessGroupDisposableDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate disposable database name: %v", err)
	}
	name := "auth_access_group_" + hex.EncodeToString(random[:])
	adminConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse maintenance database URL: %v", err)
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
		t.Fatalf("connect disposable database %q: %v", name, err)
	}
	t.Cleanup(func() {
		pool.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	return pool
}

func insertAuthAccessGroupTestGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) int64 {
	t.Helper()
	return insertAuthAccessGroupTestGroupWithLabel(t, ctx, pool, suffix, "")
}

func insertAuthAccessGroupTestGroupWithLabel(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	suffix string,
	label string,
) int64 {
	t.Helper()
	var id int64
	name := "Auth Access Group Test " + suffix
	if label != "" {
		name += " " + label
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (name)
		VALUES ($1)
		RETURNING id`,
		name,
	).Scan(&id); err != nil {
		t.Fatalf("insert access group: %v", err)
	}
	return id
}

func createAuthAccessGroupUserInput(suffix, label string, groupID *int64) models.CreateUserInput {
	id := time.Now().UnixNano()
	return models.CreateUserInput{
		Email:         fmt.Sprintf("auth-access-group-test-%s-%s-%d@example.invalid", suffix, label, id),
		Username:      fmt.Sprintf("auth-access-group-test-%s-%s-%d", suffix, label, id),
		Password:      "password",
		Role:          "user",
		AccessGroupID: groupID,
	}
}

func authAccessGroupDefaultColumnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'access_groups'
			  AND column_name = 'is_default'
		)`).Scan(&exists); err != nil {
		t.Fatalf("check access_groups.is_default column: %v", err)
	}
	return exists
}

func defaultAuthAccessGroupSeedID(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		SELECT id
		FROM access_groups
		WHERE name = 'Default Group'
		  AND is_default`).Scan(&id); err != nil {
		t.Fatalf("load seeded default access group: %v", err)
	}
	return id
}

func setAuthDefaultAccessGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, groupID int64) {
	t.Helper()
	clearAuthDefaultAccessGroup(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE access_groups
		SET is_default = true
		WHERE id = $1`, groupID); err != nil {
		t.Fatalf("set default access group: %v", err)
	}
}

func clearAuthDefaultAccessGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE access_groups SET is_default = false WHERE is_default`); err != nil {
		t.Fatalf("clear default access group: %v", err)
	}
}

func restoreAuthDefaultAccessGroup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, seedID int64) {
	t.Helper()
	setAuthDefaultAccessGroup(t, ctx, pool, seedID)
}

func insertAuthAccessGroupTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) int {
	t.Helper()
	var id int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, username, password_hash, role, enabled)
		VALUES ($1, $2, 'test-password-hash', 'user', true)
		RETURNING id`,
		"auth-access-group-test-"+suffix+"@example.invalid",
		"auth-access-group-test-"+suffix,
	).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	// The account's policy lives on its default-organization membership now, so
	// a directly-inserted account needs one or every policy update matches no row.
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		SELECT id, $1, 'active', 'user' FROM organizations
		WHERE is_default AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL
		ON CONFLICT (organization_id, account_id) DO NOTHING`, id); err != nil {
		t.Fatalf("seed account membership: %v", err)
	}
	return id
}
