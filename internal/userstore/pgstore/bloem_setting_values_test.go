package pgstore

// Bloem tenancy coverage moved out of Silo's setting_values_test.go so that
// file stays byte-identical to upstream.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestProfileOrganizationAndAccessGroupPersistence(t *testing.T) {
	pool, userID := newProfileIdentityTestUser(t)
	ctx := context.Background()

	var organizationID string
	var accessGroupID int64
	if err := pool.QueryRow(ctx, `
		SELECT o.id::text, g.id
		FROM organizations o
		JOIN access_groups g ON g.organization_id = o.id
		WHERE o.is_default
		ORDER BY g.id
		LIMIT 1`).Scan(&organizationID, &accessGroupID); err != nil {
		t.Fatalf("load default profile identity: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE organization_memberships m SET access_group_id=$2 FROM access_groups g WHERE g.id=$2 AND m.organization_id=g.organization_id AND m.account_id=$1 AND set_config('bloem.membership_policy_writer','v1',true) IS NOT NULL`, userID, accessGroupID); err != nil {
		t.Fatalf("assign legacy group: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')
		ON CONFLICT (organization_id, account_id) DO NOTHING`, organizationID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	store := newStore(pool, userID)
	if err := store.CreateProfile(ctx, userstore.Profile{
		ID:             "tenant-profile",
		Name:           "Tenant Profile",
		OrganizationID: organizationID,
		AccessGroupID:  &accessGroupID,
	}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	profile, err := store.GetProfile(ctx, "tenant-profile")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if profile == nil || profile.OrganizationID != organizationID || profile.AccessGroupID == nil || *profile.AccessGroupID != accessGroupID {
		t.Fatalf("profile identity = %#v", profile)
	}
	profiles, err := store.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].OrganizationID != organizationID || profiles[0].AccessGroupID == nil || *profiles[0].AccessGroupID != accessGroupID {
		t.Fatalf("listed profiles = %#v", profiles)
	}

	var legacyGroupID *int64
	if err := pool.QueryRow(ctx, `SELECT access_group_id FROM organization_memberships WHERE account_id = $1`, userID).Scan(&legacyGroupID); err != nil {
		t.Fatalf("load legacy assignment: %v", err)
	}
	if legacyGroupID == nil || *legacyGroupID != accessGroupID {
		t.Fatalf("legacy access group changed: %v", legacyGroupID)
	}
	var legacyMemberCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_memberships WHERE access_group_id = $1`, accessGroupID).Scan(&legacyMemberCount); err != nil {
		t.Fatalf("count legacy members: %v", err)
	}
	if legacyMemberCount < 1 {
		t.Fatalf("legacy member count = %d, want at least seeded account", legacyMemberCount)
	}
}

func TestProfileAccessGroupRejectsDifferentOrganization(t *testing.T) {
	pool, userID := newProfileIdentityTestUser(t)
	ctx := context.Background()
	var defaultOrganizationID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'user')
		ON CONFLICT (organization_id, account_id) DO NOTHING`, defaultOrganizationID, userID); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	var otherOrganizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status)
		VALUES ($1, 'Other', 'initializing') RETURNING id::text`, fmt.Sprintf("profile-other-%d", time.Now().UnixNano())).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("seed other organization: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id = $1`, otherOrganizationID) })
	var otherGroupID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO access_groups (name, organization_id)
		VALUES ($1, $2) RETURNING id`, fmt.Sprintf("profile-other-%d", time.Now().UnixNano()), otherOrganizationID).Scan(&otherGroupID); err != nil {
		t.Fatalf("seed other group: %v", err)
	}

	err := newStore(pool, userID).CreateProfile(ctx, userstore.Profile{
		ID:             "cross-tenant-profile",
		Name:           "Cross Tenant",
		OrganizationID: defaultOrganizationID,
		AccessGroupID:  &otherGroupID,
	})
	if err == nil {
		t.Fatal("CreateProfile accepted an access group from another organization")
	}
}

func newProfileIdentityTestUser(t *testing.T) (*pgxpool.Pool, int) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "bloem_tenancy_" + hex.EncodeToString(random)
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect maintenance database: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatalf("create disposable database: %v", err)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		admin.Close()
		t.Fatalf("parse maintenance database URL: %v", err)
	}
	config.ConnConfig.Database = name
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatalf("connect disposable database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database: %v", err)
		}
		admin.Close()
	})
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	// A freshly migrated database sits in the compatibility phase, a policy
	// freeze in which neither write path works. Declare this deployment the v1
	// writer for every later session, then hand the authority over.
	for _, setting := range []string{"bloem.membership_policy_writer", "bloem.schema_capability_writer"} {
		if _, err := admin.Exec(ctx, fmt.Sprintf("ALTER DATABASE %s SET %s = %s",
			pgx.Identifier{name}.Sanitize(), setting, pgx.Identifier{"v1"}.Sanitize())); err != nil {
			t.Fatalf("mark disposable database as policy writer: %v", err)
		}
	}
	pool.Close()
	pool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("reconnect disposable database: %v", err)
	}
	if _, err := tenancy.FinalizeMembershipPolicyAuthority(ctx, pool); err != nil {
		t.Fatalf("finalize membership policy authority: %v", err)
	}

	var userID int
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, role) VALUES ($1, 'user') RETURNING id`,
		fmt.Sprintf("profile-identity-%d", time.Now().UnixNano()),
	).Scan(&userID); err != nil {
		t.Fatalf("seed profile identity user: %v", err)
	}
	if _, err := tenancy.NewStore(pool).ProvisionDefaultMembership(ctx, userID, "user"); err != nil {
		t.Fatalf("provision profile identity membership: %v", err)
	}
	return pool, userID
}
