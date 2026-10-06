//go:build integration

package storagesource

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

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
