package storagesource_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/adminjob"
	"github.com/Silo-Server/silo-server/internal/catalog"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeletingSharedSourceLibraryRetainsHistoryDB(t *testing.T) {
	pool := storagesource.SelectionTestPool(t)
	repo := storagesource.NewRepository(pool)
	source, err := repo.CreateSource(t.Context(), storagesource.SourceConfig{PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	execSQL := func(_ *testing.T, _ *pgxpool.Pool, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	fixtureFolder := func(_ *testing.T, _ *pgxpool.Pool, id int) {
		t.Helper()
		execSQL(t, pool, `INSERT INTO media_folders(id,type,name) VALUES($1,'ebooks','Deletion fixture')`, id)
	}
	fixtureLocation := func(_ *testing.T, repo *storagesource.Repository, key uuid.UUID, folder int) storagesource.Location {
		t.Helper()
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		location, err := repo.AddLocationTx(t.Context(), tx, key, folder)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		return location
	}
	attachFile := func(_ *testing.T, _ *storagesource.Repository, revision int64, id int, ref storagesource.PersistedRef) error {
		t.Helper()
		tx, err := pool.Begin(t.Context())
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err := storagesource.AttachFilesTx(t.Context(), tx, revision, []storagesource.FileRef{{MediaFileID: id, PersistedRef: ref}}); err != nil {
			return err
		}
		return tx.Commit(t.Context())
	}
	fixtureFolder(t, pool, 91001)
	fixtureFolder(t, pool, 91002)
	first := fixtureLocation(t, repo, source.Key, 91001)
	lease, err := repo.Begin(t.Context(), source.Key, "worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, _, err := repo.NextDirectory(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplyPage(t.Context(), lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{{Id: "retained", Name: "retained.epub", LogicalPath: "Books/retained.epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 12, Revision: "v1"}}, Complete: true, ChangeToken: "first-token"}, nil); err != nil {
		t.Fatal(err)
	}
	second := fixtureLocation(t, repo, source.Key, 91002)
	// Seed an independent terminal run for the second location. Deleting the
	// first library must not change this journal or its retained inventory.
	execSQL(t, pool, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until,location_id,selection_revision)
 VALUES('6f9619ff-8b86-d011-b42d-00c04fc964ff',$1,1,'complete',1,'second',now(),$2,1)`, source.Key, second.ID)
	for _, location := range []storagesource.Location{first, second} {
		path, err := storagesource.CatalogLocation(location.ID, "retained")
		if err != nil {
			t.Fatal(err)
		}
		execSQL(t, pool, `INSERT INTO media_files(id,media_folder_id,file_path) VALUES($1,$2,$3)`, location.FolderID, location.FolderID, path)
		if err := attachFile(t, repo, 1, location.FolderID, storagesource.PersistedRef{LocationID: location.ID, EntryID: "retained", Revision: "v1"}); err != nil {
			t.Fatal(err)
		}
		execSQL(t, pool, `INSERT INTO bloem_storage_location_directories(location_id,directory_id,configuration_revision,selection_revision) VALUES($1,'root',1,1) ON CONFLICT DO NOTHING`, location.ID)
		execSQL(t, pool, `INSERT INTO bloem_storage_location_entries(location_id,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,selection_revision,last_seen_run)
	 SELECT $1,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,1,last_seen_run FROM bloem_storage_entries WHERE source_key=$2 ON CONFLICT DO NOTHING`, location.ID, source.Key)
		execSQL(t, pool, `INSERT INTO bloem_storage_location_edges(location_id,parent_directory_id,entry_id) VALUES($1,'root','retained') ON CONFLICT DO NOTHING`, location.ID)
	}
	execSQL(t, pool, `INSERT INTO bloem_storage_scan_directory_roots(run_id,directory_id,location_id,root_id)
	 SELECT $1,'root',$2,id FROM bloem_storage_selected_roots WHERE location_id=$2 ON CONFLICT DO NOTHING`, lease.RunID, first.ID)
	var secondBefore string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_array(
	 (SELECT jsonb_agg(to_jsonb(r)) FROM bloem_storage_scan_runs r WHERE location_id=$1),
	 (SELECT jsonb_agg(to_jsonb(e)) FROM bloem_storage_location_entries e WHERE location_id=$1),
	 (SELECT jsonb_agg(to_jsonb(f)) FROM bloem_storage_file_refs f WHERE location_id=$1))::text`, second.ID).Scan(&secondBefore); err != nil {
		t.Fatal(err)
	}
	worker := adminjob.NewLibraryDeleteExecutor(catalog.NewFolderRepository(pool), nil, nil)
	result, err := worker.Execute(t.Context(), adminjob.DeleteLibraryRequest{LibraryID: 91001}, nil)
	if err != nil || result == nil || result.DeletedMediaFiles != 1 {
		t.Fatal(err)
	}
	var secondAfter string
	if err := pool.QueryRow(t.Context(), `SELECT jsonb_build_array(
	 (SELECT jsonb_agg(to_jsonb(r)) FROM bloem_storage_scan_runs r WHERE location_id=$1),
	 (SELECT jsonb_agg(to_jsonb(e)) FROM bloem_storage_location_entries e WHERE location_id=$1),
	 (SELECT jsonb_agg(to_jsonb(f)) FROM bloem_storage_file_refs f WHERE location_id=$1))::text`, second.ID).Scan(&secondAfter); err != nil || secondAfter != secondBefore {
		t.Fatalf("worker changed another location: %v", err)
	}
	var state string
	var unbound bool
	var epoch int64
	var retained int
	if err := pool.QueryRow(t.Context(), `SELECT state,location_id IS NULL,lease_epoch,
	 (SELECT count(*) FROM bloem_storage_entries WHERE source_key=$2)+
	 (SELECT count(*) FROM bloem_storage_scan_runs WHERE location_id=$3)
	 FROM bloem_storage_scan_runs WHERE id=$1`, lease.RunID, source.Key, second.ID).Scan(&state, &unbound, &epoch, &retained); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || !unbound || epoch <= lease.Epoch || retained != 2 {
		t.Fatalf("history damaged: %s %v %d %d", state, unbound, epoch, retained)
	}
	if err := repo.Renew(t.Context(), lease, time.Minute); !errors.Is(err, storagesource.ErrStaleLease) {
		t.Fatalf("deleted location lease survived: %v", err)
	}
	var removed int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM library_storage_locations WHERE id=$1`, first.ID).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("location retained after deletion: %d %v", removed, err)
	}
}
