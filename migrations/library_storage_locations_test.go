package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

const (
	libraryStorageLocationsVersion = 20261007181737
	nativeSourceLineageVersion     = 20261006164315
)

// migratedToNativeOnboarding returns a disposable database migrated through
// the last native-onboarding migration and seeded with one pathless native
// library: marker, binding, default-organization entitlement and a partial
// discovery journal, as production held them.
func migratedToNativeOnboarding(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
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
	name := fmt.Sprintf("storage_locations_test_%d", time.Now().UnixNano())
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
	if _, err := provider.UpTo(t.Context(), nativeSourceLineageVersion); err != nil {
		t.Fatalf("migrate to native onboarding: %v", err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(query)[0:3], err)
		}
	}
	exec(`INSERT INTO media_folders(id,type,name) VALUES (23,'ebook','Bookwarehouse'),(17,'ebooks','Electronic Books')`)
	exec(`INSERT INTO media_folder_paths(media_folder_id,path) VALUES (17,'/books')`)
	// The native guards only admit rows written by the onboarding code; the
	// fixture reproduces that committed state directly.
	exec(`SET session_replication_role = replica`)
	exec(`INSERT INTO bloem_storage_sources(key,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled,owner_id)
		VALUES ('413183ec-ece5-4805-aac4-6c31f6ddf0df','bloem.storage.bookwarehouse','bookwarehouse','root',1,true,bloem_platform_resource_owner_id())`)
	exec(`INSERT INTO bloem_storage_bindings(id,source_key,folder_id) VALUES ('9dfac71e-570d-411f-9526-3e85de7e975c','413183ec-ece5-4805-aac4-6c31f6ddf0df',23)`)
	exec(`INSERT INTO bloem_native_libraries(folder_id,owner_id,creation_key,revision,initialized) VALUES (23,bloem_platform_resource_owner_id(),gen_random_uuid(),3,true)`)
	exec(`INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until)
		VALUES ('c0c20398-3bb3-4e52-a213-fbcfc9008fcb','413183ec-ece5-4805-aac4-6c31f6ddf0df',1,'running',1,'native-consumer:x',now())`)
	exec(`INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run)
		VALUES ('413183ec-ece5-4805-aac4-6c31f6ddf0df','epub/a','a.epub','epub/a.epub',1,1,0,'sha256:a',1,'c0c20398-3bb3-4e52-a213-fbcfc9008fcb')`)
	exec(`SET session_replication_role = origin`)
	return db, provider
}

func mustSub(t *testing.T, dir string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(FS, dir)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestLibraryStorageLocationsResetsUnpublishedNativeLibrariesPostgres(t *testing.T) {
	db, provider := migratedToNativeOnboarding(t)
	if _, err := provider.UpTo(t.Context(), libraryStorageLocationsVersion); err != nil {
		t.Fatalf("apply storage locations: %v", err)
	}
	count := func(query string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname LIKE 'bloem_native_%'`); n != 0 {
		t.Fatalf("%d bloem_native_* triggers remain", n)
	}
	if n := count(`SELECT count(*) FROM pg_proc WHERE proname LIKE 'bloem_native_%'`); n != 0 {
		t.Fatalf("%d bloem_native_* functions remain", n)
	}
	for _, table := range []string{"bloem_native_libraries", "bloem_native_publication_permits", "bloem_storage_bindings"} {
		if n := count(fmt.Sprintf(`SELECT count(*) FROM pg_class WHERE relname = '%s'`, table)); n != 0 {
			t.Fatalf("table %s remains", table)
		}
	}
	if n := count(`SELECT count(*) FROM media_folders WHERE id = 23`); n != 0 {
		t.Fatal("pathless native library survived the reset")
	}
	if n := count(`SELECT count(*) FROM media_folders WHERE id = 17`); n != 1 {
		t.Fatal("local library was removed")
	}
	if n := count(`SELECT count(*) FROM bloem_storage_sources`); n != 1 {
		t.Fatal("storage source was not retained")
	}
	if n := count(`SELECT count(*) FROM bloem_storage_scan_runs`) + count(`SELECT count(*) FROM bloem_storage_entries`); n != 0 {
		t.Fatal("discovery journal was not reset")
	}
	if n := count(`SELECT count(*) FROM pg_class WHERE relname = 'bloem_storage_ingestion'`); n != 0 {
		t.Fatal("ingestion queue table remains")
	}
	if n := count(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'bloem_storage_file_refs' AND column_name = 'location_id'`); n != 1 {
		t.Fatal("binding_id was not renamed to location_id")
	}
	// A library holds at most one storage location, and a source backs one library.
	if _, err := db.ExecContext(t.Context(), `INSERT INTO media_folders(id,type,name) VALUES (30,'ebooks','Other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES
		(gen_random_uuid(),'413183ec-ece5-4805-aac4-6c31f6ddf0df',17),(gen_random_uuid(),'413183ec-ece5-4805-aac4-6c31f6ddf0df',30)`); err == nil {
		t.Fatal("one source admitted for two libraries")
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES
		(gen_random_uuid(),'413183ec-ece5-4805-aac4-6c31f6ddf0df',17),(gen_random_uuid(),'413183ec-ece5-4805-aac4-6c31f6ddf0df',17)`); err == nil {
		t.Fatal("second storage location admitted for one library")
	}
}

func TestLibraryStorageLocationsRefusesPublishedNativeCatalogPostgres(t *testing.T) {
	db, provider := migratedToNativeOnboarding(t)
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SET session_replication_role = replica`,
		`INSERT INTO media_files(id,media_folder_id,file_path,file_size) VALUES (1,23,'bloem-storage:x',1)`,
		`INSERT INTO bloem_storage_file_refs(media_file_id,binding_id,entry_id,revision,logical_path,configuration_revision)
			VALUES (1,'9dfac71e-570d-411f-9526-3e85de7e975c','epub/a','sha256:a','epub/a.epub',1)`,
		`SET session_replication_role = origin`,
	} {
		if _, err := conn.ExecContext(t.Context(), query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	_ = conn.Close()
	if _, err := provider.UpTo(t.Context(), libraryStorageLocationsVersion); err == nil || !strings.Contains(err.Error(), "already published") {
		t.Fatalf("published native catalog reset: %v", err)
	}
	var marker int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM bloem_native_libraries`).Scan(&marker); err != nil || marker != 1 {
		t.Fatalf("refused migration changed state: %d %v", marker, err)
	}
}
