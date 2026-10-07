package storagesource

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func fixtureReference(t *testing.T) (*Repository, SourceConfig, Location, PersistedRef) {
	t.Helper()
	pool := storageTestPool(t)
	r := NewRepository(pool)
	installation := int64(91001)
	execSQL(t, pool, `INSERT INTO plugin_installations(id,plugin_id,version,install_path) VALUES(91001,'fixture','1','/synthetic-fixture')`)
	s, err := r.CreateSource(context.Background(), SourceConfig{InstallationID: &installation, PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixtureFolder(t, pool, 91001)
	location := fixtureLocation(t, r, s.Key, 91001)
	ref := PersistedRef{LocationID: location.ID, EntryID: "book", Revision: "v1", LogicalPath: "Books/Book.epub"}
	path, err := CatalogLocation(location.ID, ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES('existing-book','ebook','Existing Book')`)
	execSQL(t, pool, `INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES(91001,'existing-book',91001,$1)`, path)
	return r, s, location, ref
}

func TestFolderLocationIsTheLibrarysStorageLocation(t *testing.T) {
	r, s, location, _ := fixtureReference(t)
	got, ok, err := r.FolderLocation(t.Context(), 91001)
	if err != nil || !ok || got != location {
		t.Fatalf("location = %+v %v %v", got, ok, err)
	}
	fixtureFolder(t, r.pool, 91002)
	if _, ok, err := r.FolderLocation(t.Context(), 91002); err != nil || ok {
		t.Fatalf("local library reported a storage location: %v %v", ok, err)
	}
	// A source backs one library.
	tx, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := r.AddLocationTx(t.Context(), tx, s.Key, 91002); !errors.Is(err, ErrSourceInUse) {
		t.Fatalf("second library admitted for one source: %v", err)
	}
	if _, err := r.pool.Exec(t.Context(), `DELETE FROM bloem_storage_sources WHERE key=$1`, s.Key); err == nil {
		t.Fatal("source backing a library removed")
	}
}

func TestFileReferenceRejectsAnotherLibrary(t *testing.T) {
	r, s, _, ref := fixtureReference(t)
	other, err := r.CreateSource(context.Background(), SourceConfig{PluginID: "fixture", ProviderSourceID: "other", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true, OwnerID: s.OwnerID})
	if err != nil {
		t.Fatal(err)
	}
	fixtureFolder(t, r.pool, 91002)
	elsewhere := fixtureLocation(t, r, other.Key, 91002)
	forged := ref
	forged.LocationID = elsewhere.ID
	if err := attachFile(t, r, 1, 91001, forged); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("cross-library association accepted: %v", err)
	}
	if err := attachFile(t, r, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.FileReference(context.Background(), 91001, 91002); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("cross-library resolution: %v", err)
	}
}

func TestFileReferenceRejectsLocalPathAssociation(t *testing.T) {
	r, _, _, ref := fixtureReference(t)
	execSQL(t, r.pool, `UPDATE media_files SET file_path='/local/book.epub' WHERE id=91001`)
	if err := attachFile(t, r, 1, 91001, ref); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("local file acquired storage reference: %v", err)
	}
}

func TestUnavailableInstallationKeepsCatalogReference(t *testing.T) {
	r, _, _, ref := fixtureReference(t)
	if err := attachFile(t, r, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE plugin_installations SET enabled=false WHERE id=91001`, `DELETE FROM organization_entitlements WHERE plugin_installation_id=91001; DELETE FROM plugin_installations WHERE id=91001`} {
		execSQL(t, r.pool, query)
		_, got, err := r.FileReference(context.Background(), 91001, 91001)
		if !errors.Is(err, ErrSourceUnavailable) || got != ref {
			t.Fatalf("lost unavailable reference: %v", err)
		}
	}
}

func TestRevisionUpdatePreservesCatalogIdentity(t *testing.T) {
	r, _, _, ref := fixtureReference(t)
	if err := attachFile(t, r, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := r.pool.QueryRow(context.Background(), `SELECT to_jsonb(f)::text FROM media_files f WHERE id=91001`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	ref.Revision = "v2"
	if err := attachFile(t, r, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	_, got, err := r.FileReference(context.Background(), 91001, 91001)
	if err != nil || got != ref {
		t.Fatalf("updated ref: %v", err)
	}
	var after string
	if err := r.pool.QueryRow(context.Background(), `SELECT to_jsonb(f)::text FROM media_files f WHERE id=91001`).Scan(&after); err != nil || after != before {
		t.Fatal("revision changed catalog file")
	}
	// A file never moves to another entry.
	moved := ref
	moved.EntryID = "other-book"
	if err := attachFile(t, r, 1, 91001, moved); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("file re-pointed at another entry: %v", err)
	}
}

func TestDeletingLibraryRemovesItsLocationAndReferences(t *testing.T) {
	r, s, _, ref := fixtureReference(t)
	if err := attachFile(t, r, 1, 91001, ref); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `DELETE FROM organization_entitlements WHERE media_folder_id=91001`)
	execSQL(t, r.pool, `DELETE FROM media_folders WHERE id=91001`)
	var locations, refs int
	if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM library_storage_locations), (SELECT count(*) FROM bloem_storage_file_refs)`).Scan(&locations, &refs); err != nil || locations != 0 || refs != 0 {
		t.Fatalf("library deletion left locations=%d refs=%d: %v", locations, refs, err)
	}
	if _, err := r.Source(t.Context(), s.Key); err != nil {
		t.Fatalf("source removed with its library: %v", err)
	}
}

func TestSourceRejectsInvalidConfiguration(t *testing.T) {
	pool := storageTestPool(t)
	r := NewRepository(pool)
	for _, s := range []SourceConfig{{}, {PluginID: "x", ProviderSourceID: "x", RootEntryID: "x", ConfigurationRevision: -1}, {PluginID: "x", ProviderSourceID: "x", RootEntryID: "\x00", ConfigurationRevision: 1}} {
		if _, err := r.CreateSource(context.Background(), s); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	if _, err := r.CreateSource(context.Background(), SourceConfig{Key: uuid.New(), PluginID: "x", ProviderSourceID: "x", RootEntryID: "x", ConfigurationRevision: 1}); err != nil {
		t.Fatal(err)
	}
}
