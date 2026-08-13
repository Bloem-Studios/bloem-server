package database

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/migrations"
)

const organizationAccessGroupMigrationPreviousVersion int64 = 20260813090000

const (
	profileAccessGroupRequiredVersion int64 = 20260813130000
	profileAccessGroupPreviousVersion int64 = 20260813110000
)

func TestProfileAccessGroupRequiredPreviousVersionIsImmediatePredecessor(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "sql/*.sql")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var predecessor int64
	for _, file := range files {
		version, err := strconv.ParseInt(strings.SplitN(path.Base(file), "_", 2)[0], 10, 64)
		if err == nil && version < profileAccessGroupRequiredVersion && version > predecessor {
			predecessor = version
		}
	}
	if predecessor != profileAccessGroupPreviousVersion {
		t.Fatalf("profile-group predecessor = %d, want %d", predecessor, profileAccessGroupPreviousVersion)
	}
}

func TestProfileAccessGroupRequiredSignalFailsWithoutURL(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate database test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestProfileAccessGroupRequiredMigrationUpDownUp$", "-test.v")
	command.Env = environmentWithout("SILO_TEST_DATABASE_URL", "SILO_REQUIRE_TEST_DATABASE")
	command.Env = append(command.Env, "SILO_REQUIRE_TEST_DATABASE=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("required PostgreSQL subprocess without database passed; output=%s", output)
	}
	if !strings.Contains(string(output), "SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1") {
		t.Fatalf("required PostgreSQL subprocess failure = %v, output=%s", err, output)
	}
}

func environmentWithout(names ...string) []string {
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

func TestProfileAccessGroupRequiredMigrationUpDownUp(t *testing.T) {
	db := newDisposableMigrationDatabase(t)
	runAllMigrations(t, db)
	if err := MigrateDownTo(context.Background(), db, migrations.FS, "sql", profileAccessGroupPreviousVersion); err != nil {
		t.Fatalf("migrate to profile-group predecessor: %v", err)
	}

	organizationID := insertOrganization(t, db, "profile-group-required")
	var groupID int64
	if err := db.QueryRow(context.Background(), `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Existing non-default policy', false)
		RETURNING id`, organizationID).Scan(&groupID); err != nil {
		t.Fatalf("seed organization policy group: %v", err)
	}
	var userID int
	if err := db.QueryRow(context.Background(), `
		INSERT INTO users (username, email, password_hash, role)
		VALUES ('profile-group-required-user', 'profile-group-required@example.test', 'x', 'user')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create profile owner: %v", err)
	}
	if _, err := db.Exec(context.Background(), `
		INSERT INTO user_profiles (organization_id, access_group_id, user_id, id, name)
		VALUES ($1, NULL, $2, 'profile-group-required', 'Required')`, organizationID, userID); err != nil {
		t.Fatalf("seed nullable profile group: %v", err)
	}

	runAllMigrations(t, db)
	assertProfileGroupRequired(t, db, userID, groupID)
	_, err := db.Exec(context.Background(), `
		INSERT INTO user_profiles (organization_id, access_group_id, user_id, id, name)
		VALUES ($1, NULL, $2, 'profile-group-null-rejected', 'Rejected')`, organizationID, userID)
	assertSQLState(t, err, "23502")

	if err := MigrateDownTo(context.Background(), db, migrations.FS, "sql", profileAccessGroupPreviousVersion); err != nil {
		t.Fatalf("migrate profile-group requirement down: %v", err)
	}
	if _, err := db.Exec(context.Background(), `UPDATE user_profiles SET access_group_id=NULL WHERE user_id=$1`, userID); err != nil {
		t.Fatalf("rollback did not restore nullable profile group: %v", err)
	}
	runAllMigrations(t, db)
	assertProfileGroupRequired(t, db, userID, groupID)
}

func assertProfileGroupRequired(t *testing.T, db *pgxpool.Pool, userID int, wantGroupID int64) {
	t.Helper()
	var (
		groupID    int64
		isDefault  bool
		isNullable string
	)
	if err := db.QueryRow(context.Background(), `SELECT access_group_id FROM user_profiles WHERE user_id=$1`, userID).Scan(&groupID); err != nil {
		t.Fatalf("load migrated profile group: %v", err)
	}
	if err := db.QueryRow(context.Background(), `
		SELECT is_default FROM access_groups WHERE id=$1`, wantGroupID).Scan(&isDefault); err != nil {
		t.Fatalf("load migrated default group: %v", err)
	}
	if err := db.QueryRow(context.Background(), `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema='public' AND table_name='user_profiles' AND column_name='access_group_id'`).Scan(&isNullable); err != nil {
		t.Fatalf("load profile-group nullability: %v", err)
	}
	if groupID != wantGroupID || !isDefault || isNullable != "NO" {
		t.Fatalf("profile group/default/nullability = %d/%t/%s, want %d/true/NO", groupID, isDefault, isNullable, wantGroupID)
	}
}

func TestOrganizationAccessGroupMigrationUpDownUp(t *testing.T) {
	db := newDisposableMigrationDatabase(t)
	runAllMigrations(t, db)

	orgA := insertOrganization(t, db, "group-org-a")
	orgB := insertOrganization(t, db, "group-org-b")
	makeOrganizationDefault(t, db, orgA)
	insertDefaultGroup(t, db, orgA, "Default A")
	insertDefaultGroup(t, db, orgB, "Default B")

	_, err := db.Exec(context.Background(), `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Second A', true)`, orgA)
	assertSQLState(t, err, "23505")

	assertCrossOrganizationProfileGroupRejected(t, db, orgA, orgB)
	runMigrationDownUp(t, db, "20260813110000_organization_access_group_invariants.sql")
	assertDefaultGroupState(t, db, orgA, "Default A", true)
	assertDefaultGroupState(t, db, orgB, "Default B", true)
	_, err = db.Exec(context.Background(), `
		INSERT INTO access_groups (organization_id, name, is_default)
		VALUES ($1, 'Second B', true)`, orgB)
	assertSQLState(t, err, "23505")
}

func runAllMigrations(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if err := RunMigrations(context.Background(), db, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate latest schema: %v", err)
	}
}

func runMigrationDownUp(t *testing.T, db *pgxpool.Pool, migrationFile string) {
	t.Helper()
	if err := MigrateDownTo(context.Background(), db, migrations.FS, "sql", organizationAccessGroupMigrationPreviousVersion); err != nil {
		t.Fatalf("migrate %s down: %v", migrationFile, err)
	}
	if err := RunMigrations(context.Background(), db, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate %s back up: %v", migrationFile, err)
	}
}

func insertOrganization(t *testing.T, db *pgxpool.Pool, slug string) string {
	t.Helper()
	ctx := context.Background()
	var ownerID int
	if err := db.QueryRow(ctx, `
INSERT INTO users (username, email, password_hash, role)
VALUES ($1, $2, 'x', 'admin')
RETURNING id`, slug+"-owner", slug+"-owner@example.com").Scan(&ownerID); err != nil {
		t.Fatalf("create %s owner: %v", slug, err)
	}
	var organizationID string
	if err := db.QueryRow(ctx, `
INSERT INTO organizations (slug, name, status, owner_account_id)
VALUES ($1, $2, 'active', $3)
RETURNING id::text`, slug, fmt.Sprintf("%s organization", slug), ownerID).Scan(&organizationID); err != nil {
		t.Fatalf("create %s organization: %v", slug, err)
	}
	return organizationID
}

func makeOrganizationDefault(t *testing.T, db *pgxpool.Pool, organizationID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, `UPDATE access_groups SET is_default = false WHERE is_default`); err != nil {
		t.Fatalf("clear bootstrap default group: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE organizations SET is_default = false WHERE is_default`); err != nil {
		t.Fatalf("clear bootstrap default organization: %v", err)
	}
	if _, err := db.Exec(ctx, `UPDATE organizations SET is_default = true WHERE id = $1`, organizationID); err != nil {
		t.Fatalf("make organization default: %v", err)
	}
}

func insertDefaultGroup(t *testing.T, db *pgxpool.Pool, organizationID, name string) int64 {
	t.Helper()
	var groupID int64
	if err := db.QueryRow(context.Background(), `
INSERT INTO access_groups (organization_id, name, is_default)
VALUES ($1, $2, true)
RETURNING id`, organizationID, name).Scan(&groupID); err != nil {
		t.Fatalf("create default group %q: %v", name, err)
	}
	return groupID
}

func assertDefaultGroupState(t *testing.T, db *pgxpool.Pool, organizationID, groupName string, want bool) {
	t.Helper()
	var got bool
	if err := db.QueryRow(context.Background(), `
SELECT is_default
FROM access_groups
WHERE organization_id = $1 AND name = $2`, organizationID, groupName).Scan(&got); err != nil {
		t.Fatalf("read default group state: %v", err)
	}
	if got != want {
		t.Errorf("organization %s default state = %t, want %t", organizationID, got, want)
	}
}

func assertCrossOrganizationProfileGroupRejected(t *testing.T, db *pgxpool.Pool, organizationA, organizationB string) {
	t.Helper()
	ctx := context.Background()
	var userID int
	if err := db.QueryRow(ctx, `
INSERT INTO users (username, email, password_hash, role)
VALUES ('organization-access-group-profile', 'organization-access-group-profile@example.com', 'x', 'user')
RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create profile user: %v", err)
	}
	var groupID int64
	if err := db.QueryRow(ctx, `
INSERT INTO access_groups (organization_id, name)
VALUES ($1, 'Organization B group')
RETURNING id`, organizationB).Scan(&groupID); err != nil {
		t.Fatalf("create organization B group: %v", err)
	}
	_, err := db.Exec(ctx, `
INSERT INTO user_profiles (organization_id, access_group_id, user_id, id, name)
VALUES ($1, $2, $3, 'cross-organization', 'Cross Organization')`, organizationA, groupID, userID)
	assertSQLState(t, err, "23503")
}

func assertSQLState(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("statement succeeded, want SQLSTATE %s", want)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != want {
		t.Fatalf("statement error = %v, want SQLSTATE %s", err, want)
	}
}
