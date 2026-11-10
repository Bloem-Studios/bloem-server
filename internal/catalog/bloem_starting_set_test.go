package catalog_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

// startingSetMembers counts the library's rows in the creation-default
// bundle: in its active revision, and in every revision.
func startingSetMembers(t *testing.T, pool *pgxpool.Pool, folder int) (active, all int) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), `SELECT
	 count(*) FILTER (WHERE m.bundle_revision = b.active_revision), count(*)
	 FROM entitlement_bundle_members m JOIN entitlement_bundles b ON b.id = m.bundle_id
	 WHERE b.is_organization_creation_default AND m.media_folder_id = $1`, folder).Scan(&active, &all); err != nil {
		t.Fatal(err)
	}
	return active, all
}

func TestCreatingALibraryAddsItToTheStartingSet(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	var folder int
	if err := pool.QueryRow(t.Context(), `INSERT INTO media_folders(type,name) VALUES('audiobooks','New audiobooks') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if active, _ := startingSetMembers(t, pool, folder); active != 1 {
		t.Fatalf("new library in the starting set's active revision %d times, want 1", active)
	}
}

func TestALibraryGivenToAnOrganizationStaysOutOfTheStartingSet(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx := t.Context()
	organization := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO organizations(id,slug,name,status) VALUES($1,$2,'Owner of a library','suspended')`, organization, "owner-"+organization.String()); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var folder int
	if err := tx.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Organization movies') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	// As nativestorage's AttachCreationTx does: release the default
	// organization's grant, then hand the new library to its owner.
	if _, err := tx.Exec(ctx, `DELETE FROM organization_entitlements WHERE media_folder_id=$1 AND granted_by_service='resource-root-compatibility'`, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE media_folders SET owner_id=(SELECT id FROM resource_owners WHERE organization_id=$2) WHERE id=$1`, folder, organization); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit an organization's new library: %v", err)
	}
	if _, all := startingSetMembers(t, pool, folder); all != 0 {
		t.Fatalf("organization library in the starting set %d times, want 0", all)
	}
}

func TestDeletingALibraryRemovesItFromEveryStartingSetRevision(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx := t.Context()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('movies','Starting-set library') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO media_items(content_id,type,title) VALUES('starting-set-item','movie','Starting-set item')`,
		`INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES('starting-set-item',$1)`,
		`INSERT INTO media_files(content_id,media_folder_id,file_path) VALUES('starting-set-item',$1,'/synthetic/starting-set.mkv')`,
		// A newer revision becomes active and keeps the library, so the
		// library is in a historical revision and the active one.
		`INSERT INTO entitlement_bundle_versions(bundle_id,revision,created_by_service)
		 SELECT id,active_revision+1,'starting-set-test' FROM entitlement_bundles WHERE is_organization_creation_default`,
		`UPDATE entitlement_bundles SET active_revision=active_revision+1 WHERE is_organization_creation_default`,
		`INSERT INTO entitlement_bundle_members(bundle_id,bundle_revision,entitlement_kind,root_kind,root_owner_id,media_folder_id)
		 SELECT b.id,b.active_revision,'library_access','media_folder',f.owner_id,f.id
		 FROM entitlement_bundles b CROSS JOIN media_folders f WHERE b.is_organization_creation_default AND f.id=$1`,
	} {
		args := []any{folder}
		if !strings.Contains(query, "$1") {
			args = nil
		}
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, all := startingSetMembers(t, pool, folder); all != 2 {
		t.Fatalf("library in %d starting-set revisions before deletion, want 2", all)
	}

	if _, err := catalog.NewFolderRepository(pool).DeleteWithStats(ctx, folder, nil); err != nil {
		t.Fatalf("delete a starting-set library: %v", err)
	}
	var folders, items, files, grants, versions int
	if err := pool.QueryRow(ctx, `SELECT
	 (SELECT count(*) FROM media_folders WHERE id=$1),
	 (SELECT count(*) FROM media_items WHERE content_id='starting-set-item'),
	 (SELECT count(*) FROM media_files WHERE media_folder_id=$1),
	 (SELECT count(*) FROM organization_entitlements WHERE media_folder_id=$1),
	 (SELECT count(*) FROM entitlement_bundle_versions v JOIN entitlement_bundles b ON b.id=v.bundle_id WHERE b.is_organization_creation_default)`,
		folder).Scan(&folders, &items, &files, &grants, &versions); err != nil {
		t.Fatal(err)
	}
	if _, all := startingSetMembers(t, pool, folder); all != 0 || folders != 0 || items != 0 || files != 0 || grants != 0 {
		t.Fatalf("after deletion: bundle_members=%d folders=%d items=%d files=%d grants=%d, want all 0", all, folders, items, files, grants)
	}
	if versions != 2 {
		t.Fatalf("starting set has %d revisions after deletion, want both kept", versions)
	}
}

func TestDeleteLibraryAcceptsAStartingSetLibrary(t *testing.T) {
	pool := bloemFolderDeletionPool(t)
	ctx := t.Context()
	var folder int
	if err := pool.QueryRow(ctx, `INSERT INTO media_folders(type,name) VALUES('ebooks','Old ebooks') RETURNING id`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if active, _ := startingSetMembers(t, pool, folder); active != 1 {
		t.Fatalf("library in the starting set %d times, want 1", active)
	}
	var admin int
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,role) VALUES('starting-set-admin','admin') RETURNING id`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	handler := handlers.NewLibraryHandler(catalog.NewFolderRepository(pool), nil, nil, pool, nil)
	handler.JobRepo = adminjob.NewRepository(pool)
	job, err := handler.DeleteLibrary(ctx, folder, admin)
	if err != nil || job == nil {
		t.Fatalf("delete a starting-set library: job=%v error=%v", job, err)
	}
}
