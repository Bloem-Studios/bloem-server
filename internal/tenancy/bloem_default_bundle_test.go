package tenancy_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// bundleCatalogFolder adds a platform library to the creation-default
// entitlement bundle's active revision and returns it, plus a platform library
// outside the bundle.
func bundleCatalogFolder(t *testing.T, fixture tenancyFixture) (inBundle, outside int) {
	t.Helper()
	for _, folder := range []struct {
		name string
		dest *int
	}{{"bundle catalog " + fixture.suffix, &inBundle}, {"outside catalog " + fixture.suffix, &outside}} {
		if err := fixture.pool.QueryRow(fixture.ctx, `
			INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, folder.name).Scan(folder.dest); err != nil {
			t.Fatalf("create platform library: %v", err)
		}
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO entitlement_bundle_members (
			bundle_id, bundle_revision, entitlement_kind, root_kind, root_owner_id, media_folder_id
		)
		SELECT bundles.id, bundles.active_revision, 'library_access', 'media_folder', folders.owner_id, folders.id
		FROM entitlement_bundles AS bundles, media_folders AS folders
		WHERE bundles.is_organization_creation_default AND folders.id = $1`, inBundle); err != nil {
		t.Fatalf("add bundle member: %v", err)
	}
	return inBundle, outside
}

func entitledFolders(t *testing.T, fixture tenancyFixture, organizationID uuid.UUID) map[int]bool {
	t.Helper()
	libraries, err := resourcetenancy.NewStore(fixture.pool).ListLibraries(fixture.ctx, organizationID)
	if err != nil {
		t.Fatalf("list organization libraries: %v", err)
	}
	folders := make(map[int]bool)
	for _, library := range libraries {
		if library.AccessKind == resourcetenancy.LibraryEntitled {
			folders[int(library.FolderID)] = true
		}
	}
	return folders
}

// A newly provisioned tenant sees the platform libraries of the
// creation-default bundle, and only those. Adopting the tenant again on a
// replayed provisioning job does not revive an entitlement revoked since.
func TestTenantOrganizationReceivesCreationDefaultBundle(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	inBundle, outside := bundleCatalogFolder(t, fixture)
	input := tenancy.CreateTenantOrganizationInput{
		Name: "Bundle tenant", ExternalOperatorID: "op-bundle", ExternalServiceID: "svc-bundle-" + fixture.suffix,
		Slots: 2, Transcodes: 1,
	}
	created, err := store.CreateTenantOrganization(fixture.ctx, input)
	if err != nil {
		t.Fatalf("create tenant organization: %v", err)
	}
	folders := entitledFolders(t, fixture, created.ID)
	if !folders[inBundle] || folders[outside] {
		t.Fatalf("tenant libraries = %v, want bundle library %d and not %d", folders, inBundle, outside)
	}

	if _, err := fixture.pool.Exec(fixture.ctx, `
		UPDATE organization_entitlements SET status = 'revoked', revoked_at = now()
		WHERE organization_id = $1 AND media_folder_id = $2`, created.ID, inBundle); err != nil {
		t.Fatalf("revoke entitlement: %v", err)
	}
	if _, err := store.CreateTenantOrganization(fixture.ctx, input); err != nil {
		t.Fatalf("adopt tenant organization: %v", err)
	}
	if folders := entitledFolders(t, fixture, created.ID); folders[inBundle] {
		t.Fatal("adopting the tenant again revived a revoked entitlement")
	}
}

// A platform administrator's new organization gets the same grant, recorded
// as that administrator's.
func TestOrganizationReceivesCreationDefaultBundle(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	inBundle, outside := bundleCatalogFolder(t, fixture)
	organization, err := store.CreateOrganization(adminMutationContext(fixture), tenancy.CreateOrganizationInput{
		Name: "Bundle organization", Slug: "bundle-organization", OwnerAccountID: fixture.insertAccount(t, "bundle-owner", "user"),
	})
	if err != nil {
		t.Fatalf("create organization: %v", err)
	}
	folders := entitledFolders(t, fixture, organization.ID)
	if !folders[inBundle] || folders[outside] {
		t.Fatalf("organization libraries = %v, want bundle library %d and not %d", folders, inBundle, outside)
	}
	var grantor int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT granted_by_account_id FROM organization_entitlements
		WHERE organization_id = $1 AND media_folder_id = $2`, organization.ID, inBundle).Scan(&grantor); err != nil {
		t.Fatalf("read entitlement grantor: %v", err)
	}
	if grantor != fixture.adminID {
		t.Fatalf("entitlement grantor = %d, want administrator %d", grantor, fixture.adminID)
	}
}

// Without an active creation-default bundle, organizations are still created.
func TestTenantOrganizationCreationWithoutDefaultBundle(t *testing.T) {
	store, fixture := newTenancyFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE entitlement_bundles SET status = 'retired' WHERE is_organization_creation_default`); err != nil {
		t.Fatalf("retire default bundle: %v", err)
	}
	created, err := store.CreateTenantOrganization(fixture.ctx, tenancy.CreateTenantOrganizationInput{
		Name: "Unbundled tenant", ExternalOperatorID: "op-unbundled", ExternalServiceID: "svc-unbundled-" + fixture.suffix,
		Slots: 1, Transcodes: 0,
	})
	if err != nil {
		t.Fatalf("create tenant without a default bundle: %v", err)
	}
	if folders := entitledFolders(t, fixture, created.ID); len(folders) != 0 {
		t.Fatalf("tenant without a default bundle got libraries %v", folders)
	}
}
