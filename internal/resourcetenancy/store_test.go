package resourcetenancy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/migrations"
)

func TestStoreRequireAccessUsesTypedOwnerAndEntitlement(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	platformGrant, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, fixture.platformFolder)
	if err != nil {
		t.Fatalf("RequireAccess platform root: %v", err)
	}
	if platformGrant.Root != fixture.platformFolder || platformGrant.Owner.Kind != OwnerPlatform || platformGrant.Entitlement == nil || platformGrant.Entitlement.Status != EntitlementActive {
		t.Fatalf("platform grant = %#v", platformGrant)
	}

	organizationGrant, err := store.RequireAccess(fixture.ctx, fixture.otherTenant, fixture.organizationFolder)
	if err != nil {
		t.Fatalf("RequireAccess organization root: %v", err)
	}
	if organizationGrant.Owner.Kind != OwnerOrganization || organizationGrant.Owner.OrganizationID == nil || *organizationGrant.Owner.OrganizationID != fixture.otherTenant.OrganizationID || organizationGrant.Entitlement != nil {
		t.Fatalf("organization grant = %#v", organizationGrant)
	}

	if _, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, fixture.organizationFolder); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("wrong-organization error = %v, want ErrResourceHidden", err)
	}
	if _, err := store.RequireAccess(fixture.ctx, fixture.otherTenant, fixture.platformFolder); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("unentitled error = %v, want ErrResourceHidden", err)
	}
}

func TestStoreRequireAccessFailsClosed(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	tests := []struct {
		name   string
		tenant tenancy.Context
		root   RootRef
		err    error
	}{
		{name: "missing root", tenant: fixture.defaultTenant, root: RootRef{Kind: RootMediaFolder, ID: 999999}, err: ErrResourceHidden},
		{name: "unknown root kind", tenant: fixture.defaultTenant, root: RootRef{Kind: RootKind("caller-table"), ID: 1}, err: ErrInvalidRoot},
		{name: "zero root id", tenant: fixture.defaultTenant, root: RootRef{Kind: RootMediaFolder}, err: ErrInvalidRoot},
		{name: "missing organization", tenant: tenancy.Context{MembershipStatus: tenancy.MembershipActive}, root: fixture.platformFolder, err: ErrResourceHidden},
		{name: "suspended membership", tenant: withMembershipStatus(fixture.defaultTenant, tenancy.MembershipSuspended), root: fixture.platformFolder, err: ErrResourceHidden},
		{name: "invited membership", tenant: withMembershipStatus(fixture.defaultTenant, tenancy.MembershipInvited), root: fixture.platformFolder, err: ErrResourceHidden},
		{name: "suspended organization", tenant: withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationSuspended), root: fixture.platformFolder, err: ErrResourceHidden},
		{name: "initializing v2 organization", tenant: asV2(withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing)), root: fixture.platformFolder, err: ErrResourceHidden},
		{name: "non-default legacy initializing organization", tenant: func() tenancy.Context {
			tenant := withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing)
			tenant.OrganizationDefault = false
			return tenant
		}(), root: fixture.platformFolder, err: ErrResourceHidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.RequireAccess(fixture.ctx, tt.tenant, tt.root); !errors.Is(err, tt.err) {
				t.Fatalf("RequireAccess error = %v, want %v", err, tt.err)
			}
		})
	}

	legacyInitializing := withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing)
	legacyInitializing.Legacy = true
	if _, err := store.RequireAccess(fixture.ctx, legacyInitializing, fixture.platformFolder); err != nil {
		t.Fatalf("legacy initializing default organization denied: %v", err)
	}

	if _, err := (&Store{}).RequireAccess(fixture.ctx, fixture.defaultTenant, fixture.platformFolder); !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("missing store error = %v, want ErrResourceUnavailable", err)
	}
}

func TestStoreRequireAccessRejectsSuspendedAndRevokedEntitlements(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	var suspendedID int64
	if err := fixture.pool.QueryRow(fixture.ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Suspended grant') RETURNING id`).Scan(&suspendedID); err != nil {
		t.Fatalf("create suspended-grant root: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_entitlements SET status='suspended' WHERE organization_id=$1 AND media_folder_id=$2`, fixture.defaultTenant.OrganizationID, suspendedID); err != nil {
		t.Fatalf("suspend entitlement: %v", err)
	}
	if _, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, RootRef{Kind: RootMediaFolder, ID: suspendedID}); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("suspended entitlement error = %v, want ErrResourceHidden", err)
	}

	var revokedID int64
	if err := fixture.pool.QueryRow(fixture.ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Revoked grant') RETURNING id`).Scan(&revokedID); err != nil {
		t.Fatalf("create revoked-grant root: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organization_entitlements SET status='revoked', revoked_at=now() WHERE organization_id=$1 AND media_folder_id=$2`, fixture.defaultTenant.OrganizationID, revokedID); err != nil {
		t.Fatalf("revoke entitlement: %v", err)
	}
	if _, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, RootRef{Kind: RootMediaFolder, ID: revokedID}); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("revoked entitlement error = %v, want ErrResourceHidden", err)
	}
}

func TestStoreRootOwnerDoesNotTrustCallerIdentity(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	owner, err := store.RootOwner(fixture.ctx, fixture.organizationFolder)
	if err != nil {
		t.Fatalf("RootOwner: %v", err)
	}
	if owner.Kind != OwnerOrganization || owner.OrganizationID == nil || *owner.OrganizationID != fixture.otherTenant.OrganizationID {
		t.Fatalf("owner = %#v", owner)
	}
	if _, err := store.RootOwner(fixture.ctx, RootRef{Kind: RootPluginInstallation, ID: fixture.organizationFolder.ID}); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("cross-kind lookup error = %v, want ErrResourceHidden", err)
	}
}

func TestStoreAvailableMediaFolderIDsReturnsOnlyTenantVisibleFolders(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	got, err := store.AvailableMediaFolderIDs(fixture.ctx, fixture.defaultTenant)
	if err != nil {
		t.Fatalf("AvailableMediaFolderIDs: %v", err)
	}
	want := []int{int(fixture.platformFolder.ID), int(fixture.ownedFolder.ID)}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("AvailableMediaFolderIDs = %#v, want exactly entitled platform and organization-owned folders %#v", got, want)
	}
}

func TestStoreListLibrariesProjectsOwnedAndEntitledWithoutForeignRows(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	libraries, err := store.ListLibraries(fixture.ctx, fixture.defaultTenant.OrganizationID)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	byID := make(map[int64]LibraryProjection, len(libraries))
	for _, library := range libraries {
		byID[library.FolderID] = library
		if library.FolderID == fixture.organizationFolder.ID || library.FolderID == fixture.unentitledFolder.ID {
			t.Fatalf("foreign or unentitled library leaked: %+v", library)
		}
	}
	owned := byID[fixture.ownedFolder.ID]
	if owned.AccessKind != LibraryOwned || owned.Entitlement != nil {
		t.Fatalf("owned projection = %+v", owned)
	}
	entitled := byID[fixture.platformFolder.ID]
	if entitled.AccessKind != LibraryEntitled || entitled.Entitlement == nil || entitled.Entitlement.OrganizationID != fixture.defaultTenant.OrganizationID {
		t.Fatalf("entitled projection = %+v", entitled)
	}
}

func TestStoreLibraryEntitlementMutationsAreOrganizationBoundAndRevisionGuarded(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	updated, err := store.SetLibraryEntitlementStatus(fixture.ctx, fixture.defaultTenant.OrganizationID, fixture.platformFolder.ID, 1, EntitlementSuspended)
	if err != nil || updated.Status != EntitlementSuspended || updated.SecurityRevision != 2 {
		t.Fatalf("SetLibraryEntitlementStatus = %+v, %v", updated, err)
	}
	if _, err := store.SetLibraryEntitlementStatus(fixture.ctx, fixture.defaultTenant.OrganizationID, fixture.platformFolder.ID, 1, EntitlementActive); !errors.Is(err, ErrAuthorizationStateChanged) {
		t.Fatalf("stale update error = %v, want ErrAuthorizationStateChanged", err)
	}
	if _, err := store.SetLibraryEntitlementStatus(fixture.ctx, fixture.otherTenant.OrganizationID, fixture.platformFolder.ID, 2, EntitlementActive); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("foreign update error = %v, want ErrResourceHidden", err)
	}
	if err := store.DeleteLibraryEntitlement(fixture.ctx, fixture.defaultTenant.OrganizationID, fixture.platformFolder.ID, 2); err != nil {
		t.Fatalf("DeleteLibraryEntitlement: %v", err)
	}
	if _, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, fixture.platformFolder); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("revoked entitlement access error = %v", err)
	}
}

func TestStoreAvailableMediaFolderIDsFailsClosedForInvalidTenantOrStore(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	store := NewStore(fixture.pool)

	tests := []struct {
		name   string
		tenant tenancy.Context
	}{
		{name: "missing organization", tenant: tenancy.Context{MembershipStatus: tenancy.MembershipActive}},
		{name: "missing account", tenant: withAccountID(fixture.defaultTenant, 0)},
		{name: "zero policy revision", tenant: withPolicyRevision(fixture.defaultTenant, 0)},
		{name: "zero security revision", tenant: withSecurityRevision(fixture.defaultTenant, 0)},
		{name: "invited membership", tenant: withMembershipStatus(fixture.defaultTenant, tenancy.MembershipInvited)},
		{name: "suspended membership", tenant: withMembershipStatus(fixture.defaultTenant, tenancy.MembershipSuspended)},
		{name: "suspended organization", tenant: withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationSuspended)},
		{name: "initializing v2 organization", tenant: asV2(withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing))},
		{name: "non-default legacy initializing organization", tenant: func() tenancy.Context {
			tenant := withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing)
			tenant.OrganizationDefault = false
			return tenant
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.AvailableMediaFolderIDs(fixture.ctx, tt.tenant); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("AvailableMediaFolderIDs error = %v, want ErrResourceHidden", err)
			}
		})
	}

	legacyDefault := withOrganizationStatus(fixture.defaultTenant, tenancy.OrganizationInitializing)
	if _, err := store.AvailableMediaFolderIDs(fixture.ctx, legacyDefault); err != nil {
		t.Fatalf("legacy default organization availability: %v", err)
	}
	if _, err := (&Store{}).AvailableMediaFolderIDs(fixture.ctx, fixture.defaultTenant); !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("missing store error = %v, want ErrResourceUnavailable", err)
	}
	canceled, cancel := context.WithCancel(fixture.ctx)
	cancel()
	if _, err := store.AvailableMediaFolderIDs(canceled, fixture.defaultTenant); !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("database query error = %v, want ErrResourceUnavailable", err)
	}
}

func withAccountID(tenant tenancy.Context, accountID int) tenancy.Context {
	tenant.AccountID = accountID
	return tenant
}

func withPolicyRevision(tenant tenancy.Context, revision int64) tenancy.Context {
	tenant.PolicyRevision = revision
	return tenant
}

func withSecurityRevision(tenant tenancy.Context, revision int64) tenancy.Context {
	tenant.SecurityRevision = revision
	return tenant
}

func TestStoreAvailableMediaFolderIDsRequiresActivePlatformEntitlement(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE organization_entitlements
		SET status='suspended'
		WHERE organization_id=$1 AND media_folder_id=$2`,
		fixture.defaultTenant.OrganizationID, fixture.platformFolder.ID,
	); err != nil {
		t.Fatalf("suspend platform entitlement: %v", err)
	}

	got, err := NewStore(fixture.pool).AvailableMediaFolderIDs(fixture.ctx, fixture.defaultTenant)
	if err != nil {
		t.Fatalf("AvailableMediaFolderIDs: %v", err)
	}
	if !slices.Equal(got, []int{int(fixture.ownedFolder.ID)}) {
		t.Fatalf("AvailableMediaFolderIDs = %#v, want only organization-owned folder after suspension", got)
	}
}

func TestCompatibilityCreateUsesPlatformOwnerAndDefaultEntitlement(t *testing.T) {
	fixture := newResourceTenancyFixture(t)

	folder, err := catalog.NewFolderRepository(fixture.pool).Create(fixture.ctx, catalog.CreateFolderInput{
		Paths: []string{"/tmp/resource-tenancy-library"},
		Type:  "movies",
		Name:  "Compatibility repository library",
	})
	if err != nil {
		t.Fatalf("FolderRepository.Create: %v", err)
	}
	installation, err := plugins.NewInstallationStore(fixture.pool).Create(fixture.ctx, plugins.CreateInstallationInput{
		PluginID:     "silo.test.repository-compatibility",
		Version:      "1.0.0",
		InstallPath:  "/tmp/repository-compatibility",
		Enabled:      true,
		UpdatePolicy: "manual",
	})
	if err != nil {
		t.Fatalf("InstallationStore.Create: %v", err)
	}

	store := NewStore(fixture.pool)
	for _, root := range []RootRef{
		{Kind: RootMediaFolder, ID: int64(folder.ID)},
		{Kind: RootPluginInstallation, ID: int64(installation.ID)},
	} {
		grant, err := store.RequireAccess(fixture.ctx, fixture.defaultTenant, root)
		if err != nil {
			t.Fatalf("RequireAccess(%#v): %v", root, err)
		}
		if grant.Owner.Kind != OwnerPlatform || grant.Entitlement == nil || grant.Entitlement.Status != EntitlementActive {
			t.Fatalf("compatibility grant = %#v", grant)
		}
	}

	for label, value := range map[string]any{"folder": folder, "installation": installation} {
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s compatibility result: %v", label, err)
		}
		lower := strings.ToLower(string(payload))
		for _, forbidden := range []string{"owner_id", "organization_id", "entitlement", "bundle"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s compatibility result exposes %q: %s", label, forbidden, payload)
			}
		}
	}

	var memberCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM entitlement_bundle_members`).Scan(&memberCount); err != nil {
		t.Fatalf("count frozen bundle members: %v", err)
	}
	if memberCount != 1 {
		t.Fatalf("compatibility repository creates changed frozen bundle member count to %d, want 1 builtin member", memberCount)
	}
}

type resourceTenancyFixture struct {
	ctx                context.Context
	pool               *pgxpool.Pool
	defaultTenant      tenancy.Context
	otherTenant        tenancy.Context
	platformFolder     RootRef
	ownedFolder        RootRef
	unentitledFolder   RootRef
	organizationFolder RootRef
}

func newResourceTenancyFixture(t *testing.T) resourceTenancyFixture {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required when SILO_REQUIRE_TEST_DATABASE=1")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set; skipping local PostgreSQL test")
	}
	ctx := context.Background()
	pool := newResourceTenancyTestDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate resource tenancy fixture: %v", err)
	}

	defaultTenant := activateResourceTenant(t, ctx, pool, "resource-default", true)
	otherTenant := activateResourceTenant(t, ctx, pool, "resource-other", false)

	var platformFolderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Platform root') RETURNING id`).Scan(&platformFolderID); err != nil {
		t.Fatalf("create platform root: %v", err)
	}
	var unentitledFolderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', 'Unentitled platform root') RETURNING id`).Scan(&unentitledFolderID); err != nil {
		t.Fatalf("create unentitled platform root: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM organization_entitlements WHERE organization_id=$1 AND media_folder_id=$2`, defaultTenant.OrganizationID, unentitledFolderID); err != nil {
		t.Fatalf("remove default entitlement for unentitled platform root: %v", err)
	}

	var defaultOwnerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind='organization' AND organization_id=$1`, defaultTenant.OrganizationID).Scan(&defaultOwnerID); err != nil {
		t.Fatalf("load default organization owner: %v", err)
	}
	var ownedFolderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', 'Default organization root', $1) RETURNING id`, defaultOwnerID).Scan(&ownedFolderID); err != nil {
		t.Fatalf("create default organization root: %v", err)
	}

	var otherOwnerID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM resource_owners WHERE kind='organization' AND organization_id=$1`, otherTenant.OrganizationID).Scan(&otherOwnerID); err != nil {
		t.Fatalf("load other organization owner: %v", err)
	}
	var organizationFolderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders (type, name, owner_id) VALUES ('movies', 'Organization root', $1) RETURNING id`, otherOwnerID).Scan(&organizationFolderID); err != nil {
		t.Fatalf("create organization root: %v", err)
	}

	return resourceTenancyFixture{
		ctx:                ctx,
		pool:               pool,
		defaultTenant:      defaultTenant,
		otherTenant:        otherTenant,
		platformFolder:     RootRef{Kind: RootMediaFolder, ID: platformFolderID},
		ownedFolder:        RootRef{Kind: RootMediaFolder, ID: ownedFolderID},
		unentitledFolder:   RootRef{Kind: RootMediaFolder, ID: unentitledFolderID},
		organizationFolder: RootRef{Kind: RootMediaFolder, ID: organizationFolderID},
	}
}

func activateResourceTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, label string, useDefault bool) tenancy.Context {
	t.Helper()
	var accountID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', 'admin', true)
		RETURNING id`, label, label+"@example.test").Scan(&accountID); err != nil {
		t.Fatalf("create %s account: %v", label, err)
	}

	var organizationID uuid.UUID
	if useDefault {
		if err := pool.QueryRow(ctx, `UPDATE organizations SET status='active', owner_account_id=$1 WHERE is_default RETURNING id`, accountID).Scan(&organizationID); err != nil {
			t.Fatalf("activate default organization: %v", err)
		}
	} else {
		if err := pool.QueryRow(ctx, `
			INSERT INTO organizations (slug, name, status, owner_account_id, is_default)
			VALUES ($1, $2, 'active', $3, false)
			RETURNING id`, label, label, accountID).Scan(&organizationID); err != nil {
			t.Fatalf("create %s organization: %v", label, err)
		}
	}

	var membershipID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO organization_memberships (organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', 'admin')
		ON CONFLICT (organization_id, account_id)
		DO UPDATE SET status='active', legacy_role='admin'
		RETURNING id`, organizationID, accountID).Scan(&membershipID); err != nil {
		t.Fatalf("create %s membership: %v", label, err)
	}
	return tenancy.Context{
		OrganizationID:      organizationID,
		MembershipID:        membershipID,
		AccountID:           accountID,
		OrganizationStatus:  tenancy.OrganizationActive,
		MembershipStatus:    tenancy.MembershipActive,
		PolicyRevision:      1,
		SecurityRevision:    1,
		Legacy:              useDefault,
		OrganizationDefault: useDefault,
	}
}

func withMembershipStatus(value tenancy.Context, status tenancy.MembershipStatus) tenancy.Context {
	value.MembershipStatus = status
	return value
}

func withOrganizationStatus(value tenancy.Context, status tenancy.OrganizationStatus) tenancy.Context {
	value.OrganizationStatus = status
	return value
}

func asV2(value tenancy.Context) tenancy.Context {
	value.Legacy = false
	return value
}

func newResourceTenancyTestDatabase(t *testing.T, ctx context.Context, dsn string) *pgxpool.Pool {
	t.Helper()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatalf("generate database name: %v", err)
	}
	name := "vondel_resource_tenancy_" + hex.EncodeToString(random[:])

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
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, name)
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Errorf("drop disposable database %q: %v", name, err)
		}
		admin.Close()
	})
	return pool
}
