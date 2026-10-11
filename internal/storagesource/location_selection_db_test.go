package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// A location deletion must not wait for a run locked by a Begin that then
// waits for that same location's FK lock. Acquire the location before the run
// without waiting, keeping the source -> location -> run order bounded.
func TestLegacyBeginDoesNotWaitForDeletingLocationDB(t *testing.T) {
	pool := storageTestPool(t)
	source, repo := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91001)
	location := fixtureLocation(t, repo, source.Key, 91001)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(t.Context(), `SELECT id FROM library_storage_locations WHERE id=$1 FOR UPDATE`, location.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if _, err := repo.Begin(ctx, source.Key, "contender", time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("begin waited for deleting location: %v", err)
	}
}

func TestMixedLocationsPreservePublishedIdentityDB(t *testing.T) {
	repo, source, location, ref := fixtureReference(t)
	if err := attachFile(t, repo, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := repo.pool.QueryRow(t.Context(), `SELECT jsonb_build_array(to_jsonb(f),to_jsonb(i),to_jsonb(x))::text FROM media_files f JOIN media_items i ON i.content_id=f.content_id JOIN bloem_storage_file_refs x ON x.media_file_id=f.id WHERE f.id=91001`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	selections, err := repo.LocationSelections(t.Context(), 91001)
	if err != nil || len(selections) != 1 || selections[0].Location != location || len(selections[0].Roots) != 1 || selections[0].Roots[0].DirectoryID != "root" || !selections[0].Roots[0].Enabled {
		t.Fatalf("legacy selection lost: %+v %v", selections, err)
	}
	rootID := selections[0].Roots[0].ID
	if revision, err := repo.LibraryLocationRevision(t.Context(), 91001); err != nil || revision != 1 {
		t.Fatalf("initial library revision=%d: %v", revision, err)
	}
	other, err := repo.CreateSource(t.Context(), SourceConfig{PluginID: "fixture", ProviderSourceID: "additional", RootEntryID: "other-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixtureLocation(t, repo, other.Key, 91001)
	selections, err = repo.LocationSelections(t.Context(), 91001)
	if err != nil || len(selections) != 2 {
		t.Fatalf("mixed selections=%+v %v", selections, err)
	}
	var after string
	if err := repo.pool.QueryRow(t.Context(), `SELECT jsonb_build_array(to_jsonb(f),to_jsonb(i),to_jsonb(x))::text FROM media_files f JOIN media_items i ON i.content_id=f.content_id JOIN bloem_storage_file_refs x ON x.media_file_id=f.id WHERE f.id=91001`).Scan(&after); err != nil || after != before {
		t.Fatalf("attachment changed published identity: %v", err)
	}
	execSQL(t, repo.pool, `UPDATE bloem_storage_selected_roots SET enabled=false WHERE id=$1`, rootID)
	selections, err = repo.LocationSelections(t.Context(), 91001)
	if err != nil {
		t.Fatal(err)
	}
	var retained bool
	for _, selection := range selections {
		for _, root := range selection.Roots {
			if root.ID == rootID {
				retained = !root.Enabled
			}
		}
	}
	if !retained {
		t.Fatal("disabled root identity lost")
	}
	// Root persistence grants no source/installation availability by itself.
	execSQL(t, repo.pool, `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, source.Key)
	if _, got, err := repo.FileReference(t.Context(), 91001, 91001); !errors.Is(err, ErrSourceUnavailable) || got != ref {
		t.Fatalf("new selection granted unavailable source access: %+v %v", got, err)
	}
}

// Removing either obsolete uniqueness index is required: sharing a source
// must retain independent location identities and must not inherit a listing.
func TestSharedSourceHasIndependentInventoriesDB(t *testing.T) {
	pool := storageTestPool(t)
	source, repo := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91001)
	fixtureFolder(t, pool, 91002)
	first := fixtureLocation(t, repo, source.Key, 91001)
	lease, err := repo.Begin(t.Context(), source.Key, "first", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err := repo.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplyPage(t.Context(), lease, checkpoint, &storagev1.ListResponse{Complete: true, ChangeToken: "first-token"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	second := fixtureLocation(t, repo, source.Key, 91002)
	if first.ID == second.ID || first.SourceKey != second.SourceKey {
		t.Fatalf("locations not independently retained: %+v %+v", first, second)
	}
	var inherited int
	err = pool.QueryRow(t.Context(), `SELECT
	 (SELECT count(*) FROM bloem_storage_change_tokens WHERE location_id=$1)+
	 (SELECT count(*) FROM bloem_storage_location_entries WHERE location_id=$1)+
	 (SELECT count(*) FROM bloem_storage_location_directories WHERE location_id=$1)`, second.ID).Scan(&inherited)
	if err != nil || inherited != 0 {
		t.Fatalf("new location inherited inventory: %d %v", inherited, err)
	}
	if _, ok, err := repo.FolderLocation(t.Context(), 91001); err != nil || !ok {
		t.Fatalf("single-location projection failed: %v", err)
	}
	if _, err := repo.Begin(t.Context(), source.Key, "ambiguous", time.Minute); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("legacy begin accepted multiple locations: %v", err)
	}
}

func TestLibraryMayRetainSeveralSourcesDB(t *testing.T) {
	pool := storageTestPool(t)
	source, repo := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91001)
	first := fixtureLocation(t, repo, source.Key, 91001)
	other, err := repo.CreateSource(t.Context(), SourceConfig{PluginID: "fixture", ProviderSourceID: "other", RootEntryID: "other-root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	second := fixtureLocation(t, repo, other.Key, 91001)
	if first.ID == second.ID {
		t.Fatal("location identity replaced")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_storage_selected_roots WHERE location_id IN ($1,$2) AND enabled`, first.ID, second.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("configured roots not seeded: %d %v", count, err)
	}
	if _, _, err := repo.FolderLocation(t.Context(), 91001); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("legacy projection chose arbitrary location: %v", err)
	}
}

func TestCrossLocationRootsCannotPublishFileDB(t *testing.T) {
	repo, source, first, ref := fixtureReference(t)
	if err := attachFile(t, repo, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	fixtureFolder(t, repo.pool, 91002)
	second := fixtureLocation(t, repo, source.Key, 91002)
	selections, err := repo.LocationSelections(t.Context(), 91002)
	if err != nil || len(selections) != 1 || len(selections[0].Roots) != 1 {
		t.Fatalf("second roots: %v", err)
	}
	root := selections[0].Roots[0]
	if _, err := repo.pool.Exec(t.Context(), `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id) VALUES($1,91001,$2)`, first.ID, root.ID); err == nil {
		t.Fatal("root from another location authorized a file")
	}
	path, err := CatalogLocation(second.ID, "unpublished")
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, repo.pool, `INSERT INTO media_files(id,media_folder_id,file_path) VALUES(91002,91002,$1)`, path)
	if _, err := repo.pool.Exec(t.Context(), `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id) VALUES($1,91002,$2)`, second.ID, root.ID); err == nil {
		t.Fatal("membership created a catalog reference")
	}
	if _, _, err := repo.FileReference(t.Context(), 91002, 91002); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("root row granted unpublished file access: %v", err)
	}
}

func TestRunningScansRequireCurrentLocationBindingDB(t *testing.T) {
	pool := storageTestPool(t)
	source, repo := fixtureSource(t, pool)
	if _, err := repo.Begin(t.Context(), source.Key, "unattached", time.Minute); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("unattached source got lease: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES(gen_random_uuid(),$1,1,'running',1,'unbound',now())`, source.Key); err == nil {
		t.Fatal("new running generation admitted without location")
	}
	fixtureFolder(t, pool, 91001)
	location := fixtureLocation(t, repo, source.Key, 91001)
	lease, err := repo.Begin(t.Context(), source.Key, "worker", time.Minute)
	if err != nil || lease.LocationID != location.ID || lease.SelectionRevision != 1 {
		t.Fatalf("run binding lost: %+v %v", lease, err)
	}
	execSQL(t, pool, `UPDATE library_storage_locations SET selection_revision=2 WHERE id=$1`, location.ID)
	if err := repo.Renew(t.Context(), lease, time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("selection edit did not fence lease: %v", err)
	}
}
