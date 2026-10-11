package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func mixedLocationsVersion(t *testing.T) int64 {
	t.Helper()
	matches, err := fs.Glob(FS, "sql/*_bloem_mixed_library_locations.sql")
	if err != nil || len(matches) != 1 {
		t.Fatalf("mixed location migration missing: %v %v", matches, err)
	}
	version, err := strconv.ParseInt(strings.Split(strings.TrimPrefix(matches[0], "sql/"), "_")[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func legacyMixedLocationsDB(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("test database required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database = "postgres"
	admin := stdlib.OpenDB(*cfg)
	name := fmt.Sprintf("mixed_locations_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.ExecContext(context.Background(), "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(t, "sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 20261011024611); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO media_folders(id,type,name) VALUES(91001,'ebooks','Retained ebooks')`,
		`INSERT INTO bloem_storage_sources(key,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled,owner_id)
	 VALUES('413183ec-ece5-4805-aac4-6c31f6ddf0df','fixture','books','root',1,true,bloem_platform_resource_owner_id())`,
		`INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES('9dfac71e-570d-411f-9526-3e85de7e975c','413183ec-ece5-4805-aac4-6c31f6ddf0df',91001)`,
		`INSERT INTO media_items(content_id,type,title,poster_path) VALUES('retained-book','ebook','Retained book','storage-cover:retained')`,
		`INSERT INTO media_files(id,content_id,media_folder_id,file_path) VALUES(91001,'retained-book',91001,'bloem-storage:c050c967d136c46b66f8489c849a2c6af2662586f76b1395b35a0d9e4c47920a')`,
		`INSERT INTO bloem_storage_file_refs(media_file_id,location_id,entry_id,revision,logical_path,configuration_revision,cover_entry_id,cover_revision)
	 VALUES(91001,'9dfac71e-570d-411f-9526-3e85de7e975c','book','v1','Books/Book.epub',1,'cover','cover-v1')`,
		`INSERT INTO users(id,username,role) VALUES(91001,'migration-fixture','user')`,
		`INSERT INTO ebook_reader_progress(user_id,profile_id,content_id,file_id,location,progress) VALUES(91001,'fixture','retained-book',91001,'chapter:3',0.4)`,
		`INSERT INTO bloem_book_requests(id,organization_id,media_type,identity_key,title,isbn,status,requested_by_user_id,requested_by_profile_id,library_content_id,fulfilment,client_key,request_digest)
	 SELECT '5cc4b6db-1242-4d2c-8a5b-ce4c33cbd2ae',id,'book','isbn:9780000000002','Retained book','9780000000002','completed',91001,'fixture','retained-book','library','fixture','fixture' FROM organizations WHERE is_default`,
		`INSERT INTO request_integrations(id,capability_id,supported_media_types) VALUES('retained-request','request_router.v1',ARRAY['book'])`,
		`INSERT INTO bloem_ebook_request_integrations(integration_id,organization_id,media_folder_id) SELECT 'retained-request',id,91001 FROM organizations WHERE is_default`,
		`INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until)
	 VALUES('c0c20398-3bb3-4e52-a213-fbcfc9008fcb','413183ec-ece5-4805-aac4-6c31f6ddf0df',1,'complete',1,'fixture',now())`,
		`INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run)
	 VALUES('413183ec-ece5-4805-aac4-6c31f6ddf0df','book','Book.epub','Books/Book.epub',1,12,0,'v1',1,'c0c20398-3bb3-4e52-a213-fbcfc9008fcb')`,
		`INSERT INTO bloem_storage_scan_directories(run_id,directory_id,complete,processed_count) VALUES('c0c20398-3bb3-4e52-a213-fbcfc9008fcb','root',true,1)`,
		`INSERT INTO bloem_storage_change_tokens(source_key,directory_id,token,configuration_revision) VALUES('413183ec-ece5-4805-aac4-6c31f6ddf0df','root','retained-token',1)`,
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db, provider
}

func retainedLocationSnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var snapshot string
	if err := db.QueryRowContext(t.Context(), `SELECT jsonb_build_array(
	 (SELECT jsonb_agg(to_jsonb(f)) FROM media_files f WHERE id=91001),
	 (SELECT jsonb_agg(to_jsonb(i)) FROM media_items i WHERE content_id='retained-book'),
	 (SELECT jsonb_agg(to_jsonb(p)) FROM ebook_reader_progress p WHERE content_id='retained-book'),
	 (SELECT jsonb_agg(to_jsonb(r)) FROM bloem_book_requests r WHERE library_content_id='retained-book'),
	 (SELECT jsonb_agg(to_jsonb(r)) FROM bloem_ebook_request_integrations r WHERE media_folder_id=91001),
	 (SELECT jsonb_agg(to_jsonb(x)) FROM bloem_storage_file_refs x WHERE media_file_id=91001),
	 (SELECT jsonb_agg(to_jsonb(e)) FROM bloem_storage_entries e WHERE source_key='413183ec-ece5-4805-aac4-6c31f6ddf0df'),
	 (SELECT jsonb_agg(to_jsonb(d)) FROM bloem_storage_scan_directories d WHERE run_id='c0c20398-3bb3-4e52-a213-fbcfc9008fcb'),
	 (SELECT jsonb_agg(jsonb_build_array(id,source_key,folder_id)) FROM library_storage_locations WHERE folder_id=91001))::text`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestMixedLocationsMigrationPreservesPublishedIdentitiesPostgres(t *testing.T) {
	db, provider := legacyMixedLocationsDB(t)
	before := retainedLocationSnapshot(t, db)
	if _, err := provider.UpTo(t.Context(), mixedLocationsVersion(t)); err != nil {
		t.Fatal(err)
	}
	if after := retainedLocationSnapshot(t, db); after != before {
		t.Fatalf("retained catalog/progress/artwork/request identity changed")
	}
	var roots, members, tokens, runs, state int
	if err := db.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM bloem_storage_selected_roots WHERE location_id='9dfac71e-570d-411f-9526-3e85de7e975c' AND directory_id='root' AND enabled),
	 (SELECT count(*) FROM bloem_storage_file_roots WHERE media_file_id=91001),
	 (SELECT count(*) FROM bloem_storage_change_tokens WHERE location_id='9dfac71e-570d-411f-9526-3e85de7e975c' AND token='retained-token' AND selection_revision=1),
	 (SELECT count(*) FROM bloem_storage_scan_runs WHERE location_id='9dfac71e-570d-411f-9526-3e85de7e975c' AND selection_revision=1),
	 (SELECT count(*) FROM bloem_library_location_state WHERE folder_id=91001 AND revision=1)`).Scan(&roots, &members, &tokens, &runs, &state); err != nil {
		t.Fatal(err)
	}
	if roots != 1 || members != 1 || tokens != 1 || runs != 1 || state != 1 {
		t.Fatalf("backfill=%d/%d/%d/%d/%d", roots, members, tokens, runs, state)
	}
	var directories, entries, reachable int
	if err := db.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM bloem_storage_location_directories WHERE location_id='9dfac71e-570d-411f-9526-3e85de7e975c' AND directory_id='root' AND last_complete_run='c0c20398-3bb3-4e52-a213-fbcfc9008fcb'),
	 (SELECT count(*) FROM bloem_storage_location_entries WHERE location_id='9dfac71e-570d-411f-9526-3e85de7e975c' AND entry_id='book' AND revision='v1' AND selection_revision=1),
	 (SELECT count(*) FROM bloem_storage_scan_directory_roots WHERE run_id='c0c20398-3bb3-4e52-a213-fbcfc9008fcb' AND directory_id='root')`).Scan(&directories, &entries, &reachable); err != nil || directories != 1 || entries != 1 || reachable != 1 {
		t.Fatalf("inventory backfill=%d/%d/%d %v", directories, entries, reachable, err)
	}
}

func TestMixedLocationsDowngradeRefusesAtomicallyPostgres(t *testing.T) {
	for _, test := range []struct{ name, setup string }{
		{"selected-subdirectory", `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path) VALUES(gen_random_uuid(),'9dfac71e-570d-411f-9526-3e85de7e975c','collection','Collection','Collection')`},
		{"filesystem-and-plugin", `INSERT INTO media_folder_paths(media_folder_id,path) VALUES(91001,'/synthetic/local-books')`},
		{"shared-source", `INSERT INTO media_folders(id,type,name) VALUES(91002,'ebooks','Shared'); INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES(gen_random_uuid(),'413183ec-ece5-4805-aac4-6c31f6ddf0df',91002)`},
		{"several-sources", `INSERT INTO bloem_storage_sources(key,plugin_id,provider_source_id,root_entry_id,configuration_revision,owner_id) VALUES('cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed','fixture','other','root',1,bloem_platform_resource_owner_id()); INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES(gen_random_uuid(),'cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed',91001)`},
		{"unrepresented-reference", `UPDATE bloem_storage_file_roots SET enabled=false WHERE media_file_id=91001`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, provider := legacyMixedLocationsDB(t)
			if _, err := provider.UpTo(t.Context(), mixedLocationsVersion(t)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), test.setup); err != nil {
				t.Fatal(err)
			}
			before := retainedLocationSnapshot(t, db)
			var rootsBefore int
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM bloem_storage_selected_roots`).Scan(&rootsBefore); err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Down(t.Context()); err == nil {
				t.Fatal("mixed selections silently discarded on rollback")
			}
			if after := retainedLocationSnapshot(t, db); after != before {
				t.Fatal("refused downgrade changed retained data")
			}
			var roots int
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM bloem_storage_selected_roots`).Scan(&roots); err != nil || roots != rootsBefore {
				t.Fatalf("refused downgrade lost roots: %d %v", roots, err)
			}
		})
	}
}

func TestMixedLocationsDowngradePreservesLegacyRepresentationPostgres(t *testing.T) {
	db, provider := legacyMixedLocationsDB(t)
	before := retainedLocationSnapshot(t, db)
	if _, err := provider.UpTo(t.Context(), mixedLocationsVersion(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	if after := retainedLocationSnapshot(t, db); after != before {
		t.Fatal("representable rollback changed catalog identities")
	}
	var tokens, runs int
	if err := db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_change_tokens WHERE token='retained-token'),(SELECT count(*) FROM bloem_storage_scan_runs WHERE id='c0c20398-3bb3-4e52-a213-fbcfc9008fcb')`).Scan(&tokens, &runs); err != nil || tokens != 1 || runs != 1 {
		t.Fatalf("legacy history lost: %d/%d %v", tokens, runs, err)
	}
}

func TestMixedLocationsUpgradeRetainsUnattachedHistoryPostgres(t *testing.T) {
	db, provider := legacyMixedLocationsDB(t)
	for _, query := range []string{
		`INSERT INTO bloem_storage_sources(key,plugin_id,provider_source_id,root_entry_id,configuration_revision,owner_id) VALUES('cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed','fixture','deleted-library','root',1,bloem_platform_resource_owner_id())`,
		`INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until) VALUES('18b74a30-a14b-4c11-8721-839bf5ca0e1c','cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed',1,'complete',1,'historical',now())`,
		`INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES('cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed','historical','historical.epub','historical.epub',1,1,0,'v1',1,'18b74a30-a14b-4c11-8721-839bf5ca0e1c')`,
		`INSERT INTO bloem_storage_change_tokens(source_key,directory_id,token,configuration_revision) VALUES('cf5f5bc0-9e16-48d9-9b73-a7d6ce67d5ed','root','orphan-token',1)`,
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.UpTo(t.Context(), mixedLocationsVersion(t)); err != nil {
		t.Fatal(err)
	}
	var runs, entries, tokens int
	if err := db.QueryRowContext(t.Context(), `SELECT
	 (SELECT count(*) FROM bloem_storage_scan_runs WHERE id='18b74a30-a14b-4c11-8721-839bf5ca0e1c' AND location_id IS NULL AND selection_revision IS NULL AND state='complete'),
	 (SELECT count(*) FROM bloem_storage_entries WHERE entry_id='historical'),
	 (SELECT count(*) FROM bloem_storage_change_tokens WHERE token='orphan-token')`).Scan(&runs, &entries, &tokens); err != nil || runs != 1 || entries != 1 || tokens != 0 {
		t.Fatalf("orphan history=%d/%d/%d %v", runs, entries, tokens, err)
	}
}

func TestMixedLocationForeignKeysAreIndexedPostgres(t *testing.T) {
	db, provider := legacyMixedLocationsDB(t)
	if _, err := provider.UpTo(t.Context(), mixedLocationsVersion(t)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(t.Context(), `SELECT c.conrelid::regclass::text,c.conname FROM pg_constraint c
	 WHERE c.contype='f' AND c.conrelid IN (
	 'bloem_storage_selected_roots'::regclass,'bloem_storage_scan_runs'::regclass,
	 'bloem_storage_scan_directory_roots'::regclass,'bloem_storage_location_directories'::regclass,
	 'bloem_storage_location_entries'::regclass,'bloem_storage_location_edges'::regclass,
	 'bloem_storage_file_roots'::regclass,'bloem_storage_change_tokens'::regclass,
	 'bloem_storage_file_refs'::regclass)
	 AND NOT EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=c.conrelid AND i.indisvalid
	 AND i.indpred IS NULL AND i.indexprs IS NULL AND
	 ARRAY(SELECT a.k FROM unnest(i.indkey) WITH ORDINALITY a(k,pos)
	 WHERE a.pos<=cardinality(c.conkey) ORDER BY a.pos)=c.conkey)
	 ORDER BY c.conrelid::regclass::text,c.conname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, constraint string
		if err := rows.Scan(&table, &constraint); err != nil {
			t.Fatal(err)
		}
		t.Errorf("unindexed FK: %s.%s", table, constraint)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
