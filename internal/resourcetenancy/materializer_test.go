package resourcetenancy

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestMaterializeDefaultBundleIsIdempotent(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	bundleID, revision, roots := seedMaterializationBundle(t, fixture)
	materializer := NewMaterializer(fixture.pool)

	first, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "test-materializer"})
	if err != nil {
		t.Fatalf("first MaterializeDefaultBundle: %v", err)
	}
	if first.BundleID != bundleID || first.Revision != revision || first.Created != int64(len(roots)) || first.Existing != 0 {
		t.Fatalf("first result = %#v, want bundle %s revision %d created %d", first, bundleID, revision, len(roots))
	}
	before := entitlementIdentitySnapshot(t, fixture, fixture.otherTenant.OrganizationID)

	second, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "test-materializer"})
	if err != nil {
		t.Fatalf("second MaterializeDefaultBundle: %v", err)
	}
	if second.Created != 0 || second.Existing != int64(len(roots)) || second.BundleID != bundleID || second.Revision != revision {
		t.Fatalf("second result = %#v", second)
	}
	if after := entitlementIdentitySnapshot(t, fixture, fixture.otherTenant.OrganizationID); after != before {
		t.Fatalf("idempotent materialization rewrote entitlements:\n before %s\n after  %s", before, after)
	}
}

func TestMaterializeDefaultBundleSerializesConcurrentCalls(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	_, _, roots := seedMaterializationBundle(t, fixture)
	tenant := activateResourceTenant(t, fixture.ctx, fixture.pool, "resource-concurrent", false)
	materializer := NewMaterializer(fixture.pool)

	const callers = 8
	results := make(chan MaterializationResult, callers)
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := materializer.MaterializeDefaultBundle(context.Background(), tenant.OrganizationID, Actor{Service: "concurrent-test"})
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	var totalCreated int64
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent materialization: %v", err)
		}
	}
	for result := range results {
		totalCreated += result.Created
	}
	if totalCreated != int64(len(roots)) {
		t.Fatalf("concurrent created total = %d, want %d", totalCreated, len(roots))
	}
	if got := countLiveEntitlements(t, fixture, tenant.OrganizationID); got != len(roots) {
		t.Fatalf("concurrent live entitlements = %d, want %d", got, len(roots))
	}
}

func TestMaterializeDefaultBundleFreezesAppliedRevision(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	bundleID, revision, roots := seedMaterializationBundle(t, fixture)
	materializer := NewMaterializer(fixture.pool)
	if _, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "revision-test"}); err != nil {
		t.Fatalf("materialize revision 1: %v", err)
	}
	before := entitlementIdentitySnapshot(t, fixture, fixture.otherTenant.OrganizationID)

	newRoot := createPlatformFolder(t, fixture, "Revision two root")
	const nextRevision int64 = 2
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO entitlement_bundle_versions (bundle_id, revision, created_by_service)
		VALUES ($1, $2, 'revision-test')`, bundleID, nextRevision); err != nil {
		t.Fatalf("create bundle revision 2: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO entitlement_bundle_members (
			bundle_id, bundle_revision, entitlement_kind, root_kind, root_owner_id, media_folder_id
		)
		SELECT $1, $2, 'library_access', 'media_folder', owner_id, id
		FROM media_folders WHERE id=$3`, bundleID, nextRevision, newRoot.ID); err != nil {
		t.Fatalf("add revision 2 member: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE entitlement_bundles SET active_revision=$2 WHERE id=$1`, bundleID, nextRevision); err != nil {
		t.Fatalf("activate bundle revision 2: %v", err)
	}

	result, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "revision-test"})
	if err != nil {
		t.Fatalf("materialize revision 2: %v", err)
	}
	if result.Revision != nextRevision || result.Created != 1 || result.Existing != 0 {
		t.Fatalf("revision 2 result = %#v", result)
	}
	if got := entitlementIdentitySnapshotForRoots(t, fixture, fixture.otherTenant.OrganizationID, roots); got != before {
		t.Fatalf("revision 2 rewrote revision %d entitlements:\n before %s\n after  %s", revision, before, got)
	}
}

func TestMaterializeDefaultBundleRollsBackOnMemberFailure(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	_, _, roots := seedMaterializationBundle(t, fixture)
	hasPlugin := false
	for _, root := range roots {
		hasPlugin = hasPlugin || root.Kind == RootPluginInstallation
	}
	if !hasPlugin {
		t.Fatalf("materialization fixture has no plugin root: %#v", roots)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `CREATE TABLE materialization_failure_org (organization_id uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create failure trigger fixture: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `INSERT INTO materialization_failure_org (organization_id) VALUES ($1)`, fixture.otherTenant.OrganizationID); err != nil {
		t.Fatalf("seed failure trigger fixture: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		CREATE FUNCTION reject_materialization_test_plugin()
		RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.plugin_installation_id IS NOT NULL AND EXISTS (
				SELECT 1 FROM materialization_failure_org WHERE organization_id = NEW.organization_id
			) THEN
				RAISE EXCEPTION 'injected materialization failure';
			END IF;
			RETURN NEW;
		END;
		$$`); err != nil {
		t.Fatalf("create failure trigger function: %v", err)
	}
	if _, err := fixture.pool.Exec(fixture.ctx, `
		CREATE TRIGGER reject_materialization_test_plugin
		BEFORE INSERT ON organization_entitlements
		FOR EACH ROW EXECUTE FUNCTION reject_materialization_test_plugin()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	_, err := NewMaterializer(fixture.pool).MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "rollback-test"})
	if !errors.Is(err, ErrResourceUnavailable) {
		t.Fatalf("materialization failure = %v, want ErrResourceUnavailable", err)
	}
	if got := countLiveEntitlements(t, fixture, fixture.otherTenant.OrganizationID); got != 0 {
		t.Fatalf("failed materialization left %d entitlements, want 0", got)
	}
}

func TestMaterializeDefaultBundleRejectsInvalidOrganizationAndActor(t *testing.T) {
	fixture := newResourceTenancyFixture(t)
	seedMaterializationBundle(t, fixture)
	materializer := NewMaterializer(fixture.pool)

	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE organizations SET status='suspended' WHERE id=$1`, fixture.otherTenant.OrganizationID); err != nil {
		t.Fatalf("suspend organization: %v", err)
	}
	if _, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.otherTenant.OrganizationID, Actor{Service: "suspended-test"}); !errors.Is(err, ErrOrganizationUnavailable) {
		t.Fatalf("suspended organization error = %v, want ErrOrganizationUnavailable", err)
	}
	if got := countLiveEntitlements(t, fixture, fixture.otherTenant.OrganizationID); got != 0 {
		t.Fatalf("suspended organization received %d entitlements", got)
	}

	accountID := fixture.defaultTenant.AccountID
	for name, actor := range map[string]Actor{
		"missing": {},
		"both":    {AccountID: &accountID, Service: "also-service"},
		"blank":   {Service: "   "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := materializer.MaterializeDefaultBundle(fixture.ctx, fixture.defaultTenant.OrganizationID, actor); !errors.Is(err, ErrInvalidActor) {
				t.Fatalf("actor error = %v, want ErrInvalidActor", err)
			}
		})
	}
}

func seedMaterializationBundle(t *testing.T, fixture resourceTenancyFixture) (uuid.UUID, int64, []RootRef) {
	t.Helper()
	plugin := createPlatformPlugin(t, fixture, "silo.test.materialization")
	roots := []RootRef{fixture.platformFolder, plugin}
	var bundleID uuid.UUID
	var revision int64
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT id, active_revision
		FROM entitlement_bundles
		WHERE is_organization_creation_default`).Scan(&bundleID, &revision); err != nil {
		t.Fatalf("load default bundle: %v", err)
	}
	for _, root := range roots {
		switch root.Kind {
		case RootMediaFolder:
			if _, err := fixture.pool.Exec(fixture.ctx, `
				INSERT INTO entitlement_bundle_members (
					bundle_id, bundle_revision, entitlement_kind, root_kind, root_owner_id, media_folder_id
				)
				SELECT $1, $2, 'library_access', 'media_folder', owner_id, id
				FROM media_folders WHERE id=$3`, bundleID, revision, root.ID); err != nil {
				t.Fatalf("add folder bundle member: %v", err)
			}
		case RootPluginInstallation:
			if _, err := fixture.pool.Exec(fixture.ctx, `
				INSERT INTO entitlement_bundle_members (
					bundle_id, bundle_revision, entitlement_kind, root_kind, root_owner_id, plugin_installation_id
				)
				SELECT $1, $2, 'plugin_availability', 'plugin_installation', owner_id, id
				FROM plugin_installations WHERE id=$3`, bundleID, revision, root.ID); err != nil {
				t.Fatalf("add plugin bundle member: %v", err)
			}
		}
	}
	rows, err := fixture.pool.Query(fixture.ctx, `
		SELECT root_kind, COALESCE(media_folder_id::bigint, plugin_installation_id)
		FROM entitlement_bundle_members
		WHERE bundle_id=$1 AND bundle_revision=$2
		ORDER BY root_kind, COALESCE(media_folder_id::bigint, plugin_installation_id)`, bundleID, revision)
	if err != nil {
		t.Fatalf("list bundle roots: %v", err)
	}
	defer rows.Close()
	roots = roots[:0]
	for rows.Next() {
		var root RootRef
		if err := rows.Scan(&root.Kind, &root.ID); err != nil {
			t.Fatalf("scan bundle root: %v", err)
		}
		roots = append(roots, root)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate bundle roots: %v", err)
	}
	return bundleID, revision, roots
}

func createPlatformFolder(t *testing.T, fixture resourceTenancyFixture, name string) RootRef {
	t.Helper()
	var id int64
	if err := fixture.pool.QueryRow(fixture.ctx, `INSERT INTO media_folders (type, name) VALUES ('movies', $1) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("create platform folder: %v", err)
	}
	return RootRef{Kind: RootMediaFolder, ID: id}
}

func createPlatformPlugin(t *testing.T, fixture resourceTenancyFixture, pluginID string) RootRef {
	t.Helper()
	var id int64
	if err := fixture.pool.QueryRow(fixture.ctx, `
		INSERT INTO plugin_installations (plugin_id, version, install_path, enabled, update_policy, kind)
		VALUES ($1, '1.0.0', '/tmp/materializer', true, 'manual', 'plugin')
		RETURNING id`, pluginID).Scan(&id); err != nil {
		t.Fatalf("create platform plugin: %v", err)
	}
	return RootRef{Kind: RootPluginInstallation, ID: id}
}

func countLiveEntitlements(t *testing.T, fixture resourceTenancyFixture, organizationID uuid.UUID) int {
	t.Helper()
	var count int
	if err := fixture.pool.QueryRow(fixture.ctx, `
		SELECT count(*) FROM organization_entitlements
		WHERE organization_id=$1 AND status IN ('active','suspended')`, organizationID).Scan(&count); err != nil {
		t.Fatalf("count live entitlements: %v", err)
	}
	return count
}

func entitlementIdentitySnapshot(t *testing.T, fixture resourceTenancyFixture, organizationID uuid.UUID) string {
	t.Helper()
	return entitlementIdentitySnapshotForRoots(t, fixture, organizationID, nil)
}

func entitlementIdentitySnapshotForRoots(t *testing.T, fixture resourceTenancyFixture, organizationID uuid.UUID, roots []RootRef) string {
	t.Helper()
	var snapshot string
	query := `
		SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id', id,
			'root_kind', root_kind,
			'media_folder_id', media_folder_id,
			'plugin_installation_id', plugin_installation_id,
			'source_bundle_id', source_bundle_id,
			'source_bundle_revision', source_bundle_revision,
			'security_revision', security_revision
		) ORDER BY root_kind, COALESCE(media_folder_id::bigint, plugin_installation_id)), '[]'::jsonb)::text
		FROM organization_entitlements
		WHERE organization_id=$1 AND status IN ('active','suspended')`
	args := []any{organizationID}
	if len(roots) > 0 {
		var folderIDs, pluginIDs []int64
		for _, root := range roots {
			if root.Kind == RootMediaFolder {
				folderIDs = append(folderIDs, root.ID)
			} else {
				pluginIDs = append(pluginIDs, root.ID)
			}
		}
		query += ` AND (media_folder_id = ANY($2) OR plugin_installation_id = ANY($3))`
		args = append(args, folderIDs, pluginIDs)
	}
	if err := fixture.pool.QueryRow(fixture.ctx, query, args...).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot entitlements: %v", err)
	}
	return snapshot
}
