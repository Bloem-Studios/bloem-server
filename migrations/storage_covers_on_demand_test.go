package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Silo-Server/silo-server/internal/artworkkey"
)

const (
	storageFileCoversVersion     = 20261007193536
	storageCoversOnDemandVersion = 20261007220212
)

// TestStorageCoversOnDemandNamesSourceCovers checks the migration's SQL cover
// revision matches artworkkey's, so every converted poster resolves.
func TestStorageCoversOnDemandNamesSourceCovers(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	name := fmt.Sprintf("storage_covers_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	scoped := config.Copy()
	scoped.Database = name
	db := stdlib.OpenDB(*scoped)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), storageFileCoversVersion); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO media_folders(id,type,name) VALUES (24,'ebooks','Bookwarehouse')`)
	exec(`INSERT INTO bloem_storage_sources(key,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled,owner_id)
		VALUES ('413183ec-ece5-4805-aac4-6c31f6ddf0df','bloem.storage.bookwarehouse','bookwarehouse','root',1,true,bloem_platform_resource_owner_id())`)
	exec(`INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES ('9dfac71e-570d-411f-9526-3e85de7e975c','413183ec-ece5-4805-aac4-6c31f6ddf0df',24)`)
	// stored: a cover the host copied; bare: no poster yet; chosen: provider
	// artwork; plain: a book without a cover.
	exec(`INSERT INTO media_items(content_id,type,title,poster_path) VALUES
		('stored','ebook','Stored','local/ebooks/stored/poster/original.r1.webp'),
		('bare','ebook','Bare',''),
		('chosen','ebook','Chosen','tmdb/ebooks/chosen/poster/original.r1.webp'),
		('plain','ebook','Plain','')`)
	for i, id := range []string{"stored", "bare", "chosen", "plain"} {
		exec(`INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES ($1,$2,24,$3)`, 91000+i, id, "bloem-storage:"+id)
		cover := sql.NullString{String: "cover/" + id, Valid: id != "plain"}
		revision := sql.NullString{String: "cover:" + id + "-1", Valid: id != "plain"}
		exec(`INSERT INTO bloem_storage_file_refs(media_file_id,location_id,entry_id,revision,logical_path,configuration_revision,cover_entry_id,cover_revision)
			VALUES ($1,'9dfac71e-570d-411f-9526-3e85de7e975c',$2,'v1',$2,1,$3,$4)`, 91000+i, "epub/"+id, cover, revision)
	}

	if _, err := provider.UpTo(t.Context(), storageCoversOnDemandVersion); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"stored": artworkkey.StorageCoverPath("stored", "cover/stored", "cover:stored-1"),
		"bare":   artworkkey.StorageCoverPath("bare", "cover/bare", "cover:bare-1"),
		"chosen": "tmdb/ebooks/chosen/poster/original.r1.webp",
		"plain":  "",
	}
	for id, expected := range want {
		var poster string
		if err := db.QueryRowContext(t.Context(), `SELECT COALESCE(poster_path,'') FROM media_items WHERE content_id=$1`, id).Scan(&poster); err != nil || poster != expected {
			t.Fatalf("%s poster = %q, want %q (%v)", id, poster, expected, err)
		}
	}
	// The copied cover's objects are queued for deletion.
	var queued int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM artwork_revision_gc_candidates WHERE original_path='local/ebooks/stored/poster/original.r1.webp'`).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("stored cover queued %d times: %v", queued, err)
	}
	var columns int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_name='bloem_storage_file_refs' AND column_name IN ('cover_fetched_revision','cover_claimed_until')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("backfill columns remain: %d %v", columns, err)
	}
}
