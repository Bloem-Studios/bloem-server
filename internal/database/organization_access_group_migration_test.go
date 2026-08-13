package database

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/migrations"
)

const organizationAccessGroupMigrationPreviousVersion int64 = 20260813090000

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
	assertDefaultGroupState(t, db, orgB, "Default B", false)
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
