package database

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/migrations"
)

const (
	resourceTenancyVersion           int64 = 20260813090000
	resourceTenancyPreviousMigration int64 = 20260812190000
)

func TestResourceTenancyMigrationPreviousVersionIsImmediatePredecessor(t *testing.T) {
	files, err := fs.Glob(migrations.FS, "sql/*.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	var predecessor int64
	for _, file := range files {
		versionText, _, ok := strings.Cut(path.Base(file), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err == nil && version < resourceTenancyVersion && version > predecessor {
			predecessor = version
		}
	}
	if predecessor != resourceTenancyPreviousMigration {
		t.Fatalf("resource-tenancy predecessor = %d, want %d", predecessor, resourceTenancyPreviousMigration)
	}
}

func TestResourceTenancyMigrationBackfillAndRollback(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate initial schema: %v", err)
	}
	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", resourceTenancyPreviousMigration); err != nil {
		t.Fatalf("migrate to resource-tenancy predecessor: %v", err)
	}

	for index := 1; index <= 2; index++ {
		if _, err := pool.Exec(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', $1)`, fmt.Sprintf("Legacy library %d", index)); err != nil {
			t.Fatalf("seed media folder %d: %v", index, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO plugin_installations (plugin_id, version, install_path, enabled, update_policy, kind)
		VALUES ('silo.test.resource-tenancy', '1.0.0', '/tmp/resource-tenancy-plugin', true, 'manual', 'plugin')`); err != nil {
		t.Fatalf("seed plugin installation: %v", err)
	}
	before := snapshotResourceTenancyLegacyRoots(ctx, t, pool, false)

	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate resource tenancy: %v", err)
	}
	assertResourceTenancyCoverage(ctx, t, pool)
	after := snapshotResourceTenancyLegacyRoots(ctx, t, pool, true)
	if after != before {
		t.Fatalf("resource tenancy changed legacy roots:\n got %s\nwant %s", after, before)
	}

	if err := MigrateDownTo(ctx, pool, migrations.FS, "sql", resourceTenancyPreviousMigration); err != nil {
		t.Fatalf("migrate resource tenancy down: %v", err)
	}
	assertResourceTenancyBoundaryRemoved(ctx, t, pool)
	if got := snapshotResourceTenancyLegacyRoots(ctx, t, pool, false); got != before {
		t.Fatalf("down migration changed legacy roots:\n got %s\nwant %s", got, before)
	}

	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("re-migrate resource tenancy: %v", err)
	}
	assertResourceTenancyCoverage(ctx, t, pool)
}

func TestResourceTenancyMigrationCleanInstallAndCompatibilityWrites(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate clean install: %v", err)
	}
	assertResourceTenancyCoverage(ctx, t, pool)

	var folderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Compatibility library') RETURNING id`).Scan(&folderID); err != nil {
		t.Fatalf("compatibility folder insert: %v", err)
	}
	assertCompatibilityRootEntitled(ctx, t, pool, "media_folder", int64(folderID))

	var pluginID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO plugin_installations (plugin_id, version, install_path, enabled, update_policy, kind)
		VALUES ('silo.test.compatibility-root', '1.0.0', '/tmp/compatibility-root', true, 'manual', 'plugin')
		RETURNING id`).Scan(&pluginID); err != nil {
		t.Fatalf("compatibility plugin insert: %v", err)
	}
	assertCompatibilityRootEntitled(ctx, t, pool, "plugin_installation", pluginID)

	var bundleMembers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_bundle_members`).Scan(&bundleMembers); err != nil {
		t.Fatalf("count frozen bundle members: %v", err)
	}
	var preexistingRoots int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_folders WHERE id <> $1`, folderID).Scan(&preexistingRoots); err != nil {
		t.Fatalf("count preexisting folders: %v", err)
	}
	var preexistingPlugins int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plugin_installations WHERE id <> $1`, pluginID).Scan(&preexistingPlugins); err != nil {
		t.Fatalf("count preexisting plugins: %v", err)
	}
	if bundleMembers != preexistingRoots+preexistingPlugins {
		t.Fatalf("compatibility creates rewrote frozen bundle: members=%d want=%d", bundleMembers, preexistingRoots+preexistingPlugins)
	}
}

func TestResourceTenancyMigrationRejectsInvalidOwnershipAndEntitlements(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newTenantIdentityDisposableDatabase(t, ctx, dsn)
	if err := RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate clean install: %v", err)
	}

	requireStatementRejected(t, pool, ctx, `INSERT INTO resource_owners (kind) VALUES ('platform')`, "second platform owner")
	requireStatementRejected(t, pool, ctx, `INSERT INTO resource_owners (kind) VALUES ('organization')`, "organization owner without organization")

	var otherOrganizationID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (slug, name, status, is_default)
		VALUES ('resource-other', 'Resource Other', 'initializing', false)
		RETURNING id`).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("create other organization: %v", err)
	}
	var organizationOwnerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind='organization' AND organization_id=$1`, otherOrganizationID).Scan(&organizationOwnerID); err != nil {
		t.Fatalf("new organization did not receive its resource owner: %v", err)
	}
	requireStatementRejected(t, pool, ctx, `INSERT INTO resource_owners (kind, organization_id) VALUES ('organization', $1)`, "duplicate organization owner", otherOrganizationID)
	requireStatementRejected(t, pool, ctx, `UPDATE entitlement_bundles SET active_revision=999 WHERE is_organization_creation_default`, "missing active bundle revision")
	var organizationFolderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', 'Private root', $1) RETURNING id`, organizationOwnerID).Scan(&organizationFolderID); err != nil {
		t.Fatalf("create organization-owned folder: %v", err)
	}

	var defaultOrganizationID, platformOwnerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM organizations WHERE is_default`).Scan(&defaultOrganizationID); err != nil {
		t.Fatalf("load default organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind = 'platform'`).Scan(&platformOwnerID); err != nil {
		t.Fatalf("load platform owner: %v", err)
	}
	requireStatementRejected(t, pool, ctx, `
		INSERT INTO organization_entitlements (
			organization_id, entitlement_kind, root_kind, root_owner_id, root_owner_kind,
			media_folder_id, status, granted_by_service
		) VALUES ($1, 'library_access', 'media_folder', $2, 'platform', $3, 'active', 'test')`,
		"organization-owned target", defaultOrganizationID, organizationOwnerID, organizationFolderID)
	requireStatementRejected(t, pool, ctx, `
		INSERT INTO organization_entitlements (
			organization_id, entitlement_kind, root_kind, root_owner_id, root_owner_kind,
			media_folder_id, status, granted_by_service
		) VALUES ($1, 'library_access', 'media_folder', $2, 'platform', $3, 'active', 'test')`,
		"owner mismatch", defaultOrganizationID, platformOwnerID, organizationFolderID)

	var platformFolderID int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Duplicate root') RETURNING id`).Scan(&platformFolderID); err != nil {
		t.Fatalf("create platform folder: %v", err)
	}
	requireStatementRejected(t, pool, ctx, `
		INSERT INTO organization_entitlements (
			organization_id, entitlement_kind, root_kind, root_owner_id, root_owner_kind,
			media_folder_id, status, granted_by_service
		) VALUES ($1, 'library_access', 'media_folder', $2, 'platform', $3, 'suspended', 'test')`,
		"duplicate live entitlement", defaultOrganizationID, platformOwnerID, platformFolderID)
	requireStatementRejected(t, pool, ctx, `
		INSERT INTO organization_entitlements (
			organization_id, entitlement_kind, root_kind, root_owner_id, root_owner_kind,
			media_folder_id, status, granted_by_service
		) VALUES ($1, 'plugin_availability', 'plugin_installation', $2, 'platform', $3, 'active', 'test')`,
		"wrong typed root", defaultOrganizationID, platformOwnerID, platformFolderID)

	if _, err := pool.Exec(ctx, `
		UPDATE organization_entitlements
		SET status = 'revoked', revoked_at = now()
		WHERE organization_id = $1 AND media_folder_id = $2`, defaultOrganizationID, platformFolderID); err != nil {
		t.Fatalf("revoke compatibility entitlement: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_entitlements (
			organization_id, entitlement_kind, root_kind, root_owner_id, root_owner_kind,
			media_folder_id, status, granted_by_service
		) VALUES ($1, 'library_access', 'media_folder', $2, 'platform', $3, 'active', 'test')`,
		defaultOrganizationID, platformOwnerID, platformFolderID); err != nil {
		t.Fatalf("create live entitlement beside revoked history: %v", err)
	}
}

func assertResourceTenancyCoverage(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var platformOwners, platformFolders, platformPlugins, bundleMembers, entitlements int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM resource_owners WHERE kind = 'platform'`).Scan(&platformOwners); err != nil {
		t.Fatalf("count platform owners: %v", err)
	}
	if platformOwners != 1 {
		t.Fatalf("platform owners = %d, want 1", platformOwners)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM media_folders f JOIN resource_owners o ON o.id = f.owner_id WHERE o.kind = 'platform'`).Scan(&platformFolders); err != nil {
		t.Fatalf("count platform folders: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plugin_installations p JOIN resource_owners o ON o.id = p.owner_id WHERE o.kind = 'platform'`).Scan(&platformPlugins); err != nil {
		t.Fatalf("count platform plugins: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entitlement_bundle_members`).Scan(&bundleMembers); err != nil {
		t.Fatalf("count bundle members: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_entitlements WHERE status = 'active'`).Scan(&entitlements); err != nil {
		t.Fatalf("count active entitlements: %v", err)
	}
	want := platformFolders + platformPlugins
	if bundleMembers != want || entitlements != want {
		t.Fatalf("coverage = roots %d members %d entitlements %d", want, bundleMembers, entitlements)
	}

	var bundleRevision int64
	if err := pool.QueryRow(ctx, `SELECT active_revision FROM entitlement_bundles WHERE is_organization_creation_default`).Scan(&bundleRevision); err != nil {
		t.Fatalf("load default bundle: %v", err)
	}
	if bundleRevision != 1 {
		t.Fatalf("default bundle revision = %d, want 1", bundleRevision)
	}
}

func assertCompatibilityRootEntitled(ctx context.Context, t *testing.T, pool *pgxpool.Pool, kind string, id int64) {
	t.Helper()
	var ownerID, platformOwnerID uuid.UUID
	var entitlementCount int
	switch kind {
	case "media_folder":
		if err := pool.QueryRow(ctx, `SELECT owner_id FROM media_folders WHERE id = $1`, id).Scan(&ownerID); err != nil {
			t.Fatalf("load compatibility folder owner: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_entitlements e JOIN organizations o ON o.id=e.organization_id AND o.is_default WHERE e.media_folder_id=$1 AND e.status='active'`, id).Scan(&entitlementCount); err != nil {
			t.Fatalf("count compatibility folder entitlements: %v", err)
		}
	case "plugin_installation":
		if err := pool.QueryRow(ctx, `SELECT owner_id FROM plugin_installations WHERE id = $1`, id).Scan(&ownerID); err != nil {
			t.Fatalf("load compatibility plugin owner: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM organization_entitlements e JOIN organizations o ON o.id=e.organization_id AND o.is_default WHERE e.plugin_installation_id=$1 AND e.status='active'`, id).Scan(&entitlementCount); err != nil {
			t.Fatalf("count compatibility plugin entitlements: %v", err)
		}
	default:
		t.Fatalf("unsupported compatibility root kind %q", kind)
	}
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind='platform'`).Scan(&platformOwnerID); err != nil {
		t.Fatalf("load platform owner: %v", err)
	}
	if ownerID != platformOwnerID || entitlementCount != 1 {
		t.Fatalf("compatibility %s %d = owner %s entitlements %d, want platform %s/1", kind, id, ownerID, entitlementCount, platformOwnerID)
	}
}

func snapshotResourceTenancyLegacyRoots(ctx context.Context, t *testing.T, pool *pgxpool.Pool, removeOwner bool) string {
	t.Helper()
	folder := "to_jsonb(f)"
	plugin := "to_jsonb(p)"
	if removeOwner {
		folder += " - 'owner_id'"
		plugin += " - 'owner_id'"
	}
	query := fmt.Sprintf(`
		SELECT jsonb_build_object(
			'folders', COALESCE((SELECT jsonb_agg(%s ORDER BY f.id) FROM media_folders f), '[]'::jsonb),
			'plugins', COALESCE((SELECT jsonb_agg(%s ORDER BY p.id) FROM plugin_installations p), '[]'::jsonb)
		)::text`, folder, plugin)
	var snapshot string
	if err := pool.QueryRow(ctx, query).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot legacy resource roots: %v", err)
	}
	return snapshot
}

func assertResourceTenancyBoundaryRemoved(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, relation := range []string{
		"resource_owners", "entitlement_bundles", "entitlement_bundle_versions",
		"entitlement_bundle_members", "organization_entitlements", "resource_tenancy_migration_ledger",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			t.Fatalf("check relation %s: %v", relation, err)
		}
		if exists {
			t.Errorf("relation %s remains after down migration", relation)
		}
	}
	for _, table := range []string{"media_folders", "plugin_installations"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema='public' AND table_name=$1 AND column_name='owner_id'
			)`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s.owner_id: %v", table, err)
		}
		if exists {
			t.Errorf("%s.owner_id remains after down migration", table)
		}
	}
}

func requireStatementRejected(t *testing.T, pool *pgxpool.Pool, ctx context.Context, query, label string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, query, args...); err == nil {
		t.Fatalf("%s unexpectedly succeeded", label)
	}
}
