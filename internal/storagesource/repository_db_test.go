//go:build integration

package storagesource

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
)

func fixtureReference(t *testing.T) (*Repository, SourceConfig, Binding, PersistedRef) {
	t.Helper()
	pool := testDatabase(t, true)
	r := NewRepository(pool)
	installation := int64(91001)
	execSQL(t, pool, `INSERT INTO plugin_installations(id,plugin_id,version,install_path) VALUES(91001,'fixture','1','/synthetic-fixture')`)
	s, err := r.CreateSource(context.Background(), SourceConfig{InstallationID: &installation, PluginID: "fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fixtureFolder(t, pool, 91001)
	b, err := r.Bind(context.Background(), s.Key, 91001)
	if err != nil {
		t.Fatal(err)
	}
	run := uuid.New()
	execSQL(t, pool, `INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES($1,$2,1,'complete',1,'fixture',now())`, run, s.Key)
	execSQL(t, pool, `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES($1,'book','Book.epub','Books/Book.epub',1,12,0,'v1',1,$2)`, s.Key, run)
	ref := PersistedRef{BindingID: b.ID, EntryID: "book", Revision: "v1", LogicalPath: "Books/Book.epub"}
	location, err := CatalogLocation(b.ID, ref.EntryID)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES('existing-book','ebook','Existing Book')`)
	execSQL(t, pool, `INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES(91001,'existing-book',91001,$1)`, location)
	return r, s, b, ref
}

func TestFileReferenceRejectsAnotherLibrary(t *testing.T) {
	r, s, _, ref := fixtureReference(t)
	fixtureFolder(t, r.pool, 91002)
	other, err := r.Bind(context.Background(), s.Key, 91002)
	if err != nil {
		t.Fatal(err)
	}
	forged := ref
	forged.BindingID = other.ID
	if err = r.AttachFile(context.Background(), 91001, forged); err == nil {
		t.Fatal("cross-library association accepted")
	}
	if err = r.AttachFile(context.Background(), 91001, ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.FileReference(context.Background(), 91001, 91002); !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("cross-library resolution: %v", err)
	}
}

func TestFileReferenceRejectsLocalPathAssociation(t *testing.T) {
	r, _, _, ref := fixtureReference(t)
	execSQL(t, r.pool, `UPDATE media_files SET file_path='/local/book.epub' WHERE id=91001`)
	if err := r.AttachFile(context.Background(), 91001, ref); err == nil {
		t.Fatal("local file acquired native reference")
	}
}

func TestUnavailableInstallationKeepsCatalogReference(t *testing.T) {
	r, _, _, ref := fixtureReference(t)
	if err := r.AttachFile(context.Background(), 91001, ref); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE plugin_installations SET enabled=false WHERE id=91001`, `DELETE FROM plugin_installations WHERE id=91001`} {
		execSQL(t, r.pool, query)
		_, got, err := r.FileReference(context.Background(), 91001, 91001)
		if !errors.Is(err, ErrSourceUnavailable) || got != ref {
			t.Fatalf("lost unavailable reference: %v", err)
		}
	}
	var count int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM bloem_storage_entries`).Scan(&count); err != nil || count != 1 {
		t.Fatal("uninstall removed journal")
	}
}

func TestRevisionUpdatePreservesCatalogIdentity(t *testing.T) {
	r, s, _, ref := fixtureReference(t)
	if err := r.AttachFile(context.Background(), 91001, ref); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := r.pool.QueryRow(context.Background(), `SELECT to_jsonb(f)::text FROM media_files f WHERE id=91001`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_entries SET revision='v2' WHERE source_key=$1`, s.Key)
	ref.Revision = "v2"
	if err := r.AttachFile(context.Background(), 91001, ref); err != nil {
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
	execSQL(t, r.pool, `UPDATE bloem_storage_sources SET root_entry_id='new-root',configuration_revision=2 WHERE key=$1`, s.Key)
	if _, _, err = r.FileReference(context.Background(), 91001, 91001); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("old reference resolved under replacement root: %v", err)
	}
	if err = r.AttachFile(context.Background(), 91001, ref); err == nil {
		t.Fatal("stale discovery associated under new configuration")
	}
}

func TestMigrationPreservesExistingData(t *testing.T) {
	pool := testDatabase(t, false)
	execSQL(t, pool, `INSERT INTO users(id,email,username,password_hash,role) VALUES(91001,'storage@example.invalid','storage-test','synthetic-hash','user')`)
	execSQL(t, pool, `INSERT INTO server_settings(key,value) VALUES('storage-preservation-sentinel','encrypted-key-bound-sentinel')`)
	execSQL(t, pool, `INSERT INTO user_watch_progress(user_id,profile_id,media_item_id,position_seconds,duration_seconds) VALUES(91001,'profile','existing-film',312,900)`)
	fixtureFolder(t, pool, 91001)
	execSQL(t, pool, `INSERT INTO media_items(content_id,type,title) VALUES('existing-book','ebook','Existing Book')`)
	execSQL(t, pool, `INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES(91001,'existing-book',91001,'/existing/book.epub')`)
	execSQL(t, pool, `INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES(91001,'profile','existing-book',91001,'chapter-3',0.42)`)
	before := preservationSnapshot(t, pool)
	migration(t, pool, true)
	migration(t, pool, false)
	if after := preservationSnapshot(t, pool); after != before {
		t.Fatal("migration altered existing account/settings/progress")
	}
}

func TestMigrationRefusesPopulatedDown(t *testing.T) {
	pool := testDatabase(t, true)
	s, _ := fixtureSource(t, pool)
	if _, err := pool.Exec(context.Background(), migrationSQL(t, false)); err == nil {
		t.Fatal("populated Down succeeded")
	}
	var key uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT key FROM bloem_storage_sources`).Scan(&key); err != nil || key != s.Key {
		t.Fatalf("source lost: %v", err)
	}
}

func TestSourceBindingStableIdentity(t *testing.T) {
	pool := testDatabase(t, true)
	s, r := fixtureSource(t, pool)
	fixtureFolder(t, pool, 91001)
	b, err := r.Bind(context.Background(), s.Key, 91001)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewRepository(pool)
	again, err := restarted.Bind(context.Background(), s.Key, 91001)
	if err != nil || again != b {
		t.Fatalf("binding changed: %v", err)
	}
	if _, err = r.CreateSource(context.Background(), s); err == nil {
		t.Fatal("source identity overwritten")
	}
	if _, err = pool.Exec(context.Background(), `DELETE FROM bloem_storage_sources WHERE key=$1`, s.Key); err == nil {
		t.Fatal("bound source removed")
	}
}

func TestSourceBindingRejectsInvalidConfiguration(t *testing.T) {
	pool := testDatabase(t, true)
	r := NewRepository(pool)
	for _, s := range []SourceConfig{{}, {PluginID: "x", ProviderSourceID: "x", RootEntryID: "x", ConfigurationRevision: -1}, {PluginID: "x", ProviderSourceID: "x", RootEntryID: "\x00", ConfigurationRevision: 1}} {
		if _, err := r.CreateSource(context.Background(), s); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}
