//go:build integration

package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type onboardingLegacy struct {
	folder               int
	binding, source, run uuid.UUID
	item                 string
	files                map[string]int
}

// This is a quiescent PRE-MODE legacy fixture, created with the onboarding
// migration absent. No runtime marker or fabricated publication authority is
// seeded: Up must prove the complete legacy candidate before importing it.
func onboardingLegacyCandidate(t *testing.T, tx pgx.Tx, formats ...string) onboardingLegacy {
	t.Helper()
	c := onboardingLegacy{binding: uuid.New(), source: uuid.New(), run: uuid.New(), item: uuid.NewString(), files: map[string]int{}}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var installation int
	if err := tx.QueryRow(t.Context(), "INSERT INTO plugin_installations(plugin_id,version,install_path,enabled,runtime_generation) VALUES($1,'1','offline-fixture',true,1) RETURNING id", "legacy-"+c.source.String()).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO bloem_storage_installations(installation_id,owner_id) SELECT id,owner_id FROM plugin_installations WHERE id=$1", installation)
	exec("INSERT INTO bloem_storage_sources(key,installation_id,plugin_id,provider_source_id,root_entry_id,configuration_revision,enabled,owner_id) SELECT $1,id,plugin_id,'legacy-root','root',1,true,owner_id FROM plugin_installations WHERE id=$2", c.source, installation)
	if err := tx.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) SELECT 'ebook','Offline legacy',owner_id FROM bloem_storage_sources WHERE key=$1 RETURNING id", c.source).Scan(&c.folder); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO library_collection_groups(library_id,label,title,id,name,slug,kind) VALUES($1,'user-collections','My collections',$2,'My collections','user-collections','user_collections')", c.folder, CanonicalUserCollectionsGroupID(c.folder))
	for _, kind := range []string{"recently_added", "recently_released"} {
		exec("INSERT INTO page_sections(id,scope,library_id,section_type,title,config) VALUES($1,'home',NULL,$2,'Recent',jsonb_build_object('generated_source','home_library_recent','generated_library_id',$3::text))", uuid.NewString(), kind, fmt.Sprint(c.folder))
	}
	exec("INSERT INTO page_sections(id,scope,library_id,section_type,title) VALUES($1,'library',$2,'recently_added','Recent')", uuid.NewString(), c.folder)
	exec("INSERT INTO bloem_storage_bindings(id,source_key,folder_id) VALUES($1,$2,$3)", c.binding, c.source, c.folder)
	exec("INSERT INTO bloem_storage_scan_runs(id,source_key,configuration_revision,state,lease_epoch,owner,lease_until,completed_at) VALUES($1,$2,1,'complete',1,'offline',now()-interval '1 hour',now())", c.run, c.source)
	exec("UPDATE bloem_storage_sources SET discovery_run_id=$2 WHERE key=$1", c.source, c.run)
	if len(formats) > 0 {
		exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Legacy book')", c.item)
		exec("INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", c.item, c.folder)
	}
	for _, format := range formats {
		entry := "legacy." + format
		location, err := storagesource.CatalogLocation(c.binding, entry)
		if err != nil {
			t.Fatal(err)
		}
		exec("INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run) VALUES($1,$2,$2,$2,1,12,1791244800000000000,'r1',1,$3)", c.source, entry, c.run)
		var file int
		if err := tx.QueryRow(t.Context(), "INSERT INTO media_files(content_id,media_folder_id,file_path,canonical_root_path,observed_root_path,container,file_size,file_modified_at,probe_source,content_group_key) VALUES($1,$2,$3,$3,$3,$4,12,'2026-10-06 UTC','native',$1) RETURNING id", c.item, c.folder, location, format).Scan(&file); err != nil {
			t.Fatal(err)
		}
		c.files[format] = file
		exec("INSERT INTO bloem_storage_file_refs(media_file_id,binding_id,entry_id,revision,logical_path,configuration_revision) VALUES($1,$2,$3,'r1',$3,1)", file, c.binding, entry)
	}
	return c
}

func TestNativeOnboardingMigrationLegacyDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	up, _ := onboardingMigrationSQL(t)
	for _, formats := range [][]string{nil, {"epub"}, {"epub", "pdf"}} {
		t.Run(fmt.Sprint(formats), func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			c := onboardingLegacyCandidate(t, tx, formats...)
			if len(formats) == 0 {
				// Empty bound import needs initialization witnesses, but does
				// not manufacture discovery provenance for nonexistent files.
				if _, err = tx.Exec(t.Context(), "UPDATE bloem_storage_sources SET discovery_run_id=NULL WHERE key=$1", c.source); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(t.Context(), "DELETE FROM bloem_storage_scan_runs WHERE id=$1", c.run); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = tx.Exec(t.Context(), up); err != nil {
				t.Fatal("valid offline legacy import refused", err)
			}
			var initialized bool
			var revision int64
			if err = tx.QueryRow(t.Context(), "SELECT initialized,revision FROM bloem_native_libraries WHERE folder_id=$1", c.folder).Scan(&initialized, &revision); err != nil || !initialized || revision != 3 {
				t.Fatal("legacy candidate did not retain same ID and import L3")
			}
			if !NativeStorageSchemaReady(t.Context(), tx) {
				t.Fatal("legacy import graph differs")
			}
			var bindings int
			if err = tx.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_bindings WHERE id=$1 AND folder_id=$2", c.binding, c.folder).Scan(&bindings); err != nil || bindings != 1 {
				t.Fatal("legacy binding changed")
			}
		})
	}
}

func TestNativeOnboardingMigrationCandidateRefusalDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	up, _ := onboardingMigrationSQL(t)
	cases := []string{"missing-epub", "missing-pdf", "missing-both", "drop", "extra-own", "extra-parent", "bad-root", "null-container", "bad-probe", "null-size", "bad-mtime", "bad-ref", "fileless-member", "missing-witness", "active-run", "other-folder-root", "other-folder-group", "wrong-run-configuration", "wrong-run-source"}
	for _, category := range cases {
		t.Run(category, func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			// The valid earlier sibling must not be imported before a later invalid
			// candidate is checked. Both candidates' complete snapshots are compared.
			onboardingLegacyCandidate(t, tx, "epub")
			c := onboardingLegacyCandidate(t, tx, "epub", "pdf")
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch category {
			case "missing-epub":
				exec("UPDATE media_files SET missing_since=now() WHERE id=$1", c.files["epub"])
			case "missing-pdf":
				exec("UPDATE media_files SET missing_since=now() WHERE id=$1", c.files["pdf"])
			case "missing-both":
				exec("UPDATE media_files SET missing_since=now() WHERE media_folder_id=$1", c.folder)
			case "drop":
				var user int
				if err := tx.QueryRow(t.Context(), "INSERT INTO users(username,password_hash,role) VALUES($1,'fixture','user') RETURNING id", uuid.NewString()).Scan(&user); err != nil {
					t.Fatal(err)
				}
				var profile string
				profile = uuid.NewString()
				exec("INSERT INTO user_profiles(id,user_id,name) VALUES($1,$2,'Offline profile')", profile, user)
				exec("INSERT INTO user_dropped_series(user_id,profile_id,series_id) VALUES($1,$2,$3)", user, profile, c.item)
			case "extra-own":
				parent := uuid.NewString()
				exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'movie','Parent')", parent)
				exec("INSERT INTO media_extras(content_id,parent_id,kind) VALUES($1,$2,'trailer')", c.item, parent)
			case "extra-parent":
				exec("INSERT INTO media_extras(content_id,parent_id,kind) VALUES($1,$2,'trailer')", uuid.NewString(), c.item)
			case "bad-root":
				exec("UPDATE media_files SET canonical_root_path='wrong' WHERE id=$1", c.files["epub"])
			case "null-container":
				exec("UPDATE media_files SET container=NULL WHERE id=$1", c.files["epub"])
			case "bad-probe":
				exec("UPDATE media_files SET probe_source='ffprobe' WHERE id=$1", c.files["epub"])
			case "null-size":
				exec("UPDATE media_files SET file_size=NULL WHERE id=$1", c.files["epub"])
			case "bad-mtime":
				exec("UPDATE media_files SET file_modified_at='2000-01-01 UTC' WHERE id=$1", c.files["epub"])
			case "bad-ref":
				exec("UPDATE bloem_storage_file_refs SET revision='wrong' WHERE media_file_id=$1", c.files["epub"])
			case "fileless-member":
				k := uuid.NewString()
				exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Fileless')", k)
				exec("INSERT INTO media_item_libraries(content_id,media_folder_id) VALUES($1,$2)", k, c.folder)
			case "missing-witness":
				exec("DELETE FROM page_section_scope_revisions WHERE scope='library' AND library_id=$1", c.folder)
			case "other-folder-root", "other-folder-group":
				var local int
				if err := tx.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Ordinary structural owner') RETURNING id").Scan(&local); err != nil {
					t.Fatal(err)
				}
				if category == "other-folder-root" {
					exec("INSERT INTO media_item_roots(media_folder_id,canonical_root_path,content_id) VALUES($1,'/ordinary/book',$2)", local, c.item)
				} else {
					exec("INSERT INTO media_item_groups(media_folder_id,group_key_version,content_group_key,content_id) VALUES($1,1,'ordinary-book',$2)", local, c.item)
				}
			case "wrong-run-configuration":
				exec("UPDATE bloem_storage_scan_runs SET configuration_revision=2 WHERE id=$1", c.run)
			case "wrong-run-source":
				foreign := onboardingLegacyCandidate(t, tx)
				// Both individual run FKs remain legal, but this run belongs to
				// the other source. Entry/ref/source configuration still agrees.
				exec("UPDATE bloem_storage_sources SET discovery_run_id=$2 WHERE key=$1", c.source, foreign.run)
				exec("UPDATE bloem_storage_entries SET last_seen_run=$2 WHERE source_key=$1", c.source, foreign.run)
			case "active-run":
				exec("UPDATE bloem_storage_scan_runs SET state='running' WHERE id=$1", c.run)
			}
			before := onboardingSnapshot(t, tx)
			exec("SAVEPOINT migration_attempt")
			_, err = tx.Exec(t.Context(), up)
			if err == nil {
				t.Fatal("ambiguous offline candidate was imported", category)
			}
			onboardingState(t, err, "BN002")
			exec("ROLLBACK TO SAVEPOINT migration_attempt")
			if after := onboardingSnapshot(t, tx); after != before {
				t.Fatal("failed Up changed complete logical rows, objects or Goose versions")
			}
		})
	}
}

// Each retained graph case uses real FK parents. The parent marker/source may
// also refuse Down; exact per-table Down predicates are separately frozen by
// the migration contract test. This exercises complete retained rows and
// rollback without claiming that FK children can exist without their parents.
func TestNativeOnboardingMigrationDownRetainedGraphDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	up, down := onboardingMigrationSQL(t)
	for _, category := range []string{"binding", "ref", "entry", "run", "directory", "cursor", "ingestion-complete", "ingestion-expired", "claim-expired"} {
		t.Run(category, func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			c := onboardingLegacyCandidate(t, tx, "epub")
			if category == "directory" || category == "cursor" {
				exec("INSERT INTO bloem_storage_scan_directories(run_id,directory_id,complete) VALUES($1,'root',true)", c.run)
			}
			if category == "cursor" {
				exec("INSERT INTO bloem_storage_scan_cursors(run_id,directory_id,cursor_sha256,cursor,page_sha256) VALUES($1,'root',decode(repeat('00',32),'hex'),'retained',decode(repeat('00',32),'hex'))", c.run)
			}
			if category == "ingestion-complete" || category == "ingestion-expired" {
				exec("INSERT INTO bloem_storage_ingestion(run_id,binding_id,lease_epoch,owner,lease_until,last_entry_id,complete) VALUES($1,$2,1,'retained',now()-interval '1 hour','legacy.epub',$3)", c.run, c.binding, category == "ingestion-complete")
			}
			if _, err = tx.Exec(t.Context(), up); err != nil {
				t.Fatal(err)
			}
			if category == "claim-expired" {
				exec("INSERT INTO bloem_storage_ingestion(run_id,binding_id,lease_epoch,owner,lease_until,pending_entry_id,pending_revision,pending_token) VALUES($1,$2,1,'retained',now()-interval '1 hour','legacy.epub','r1',$3)", c.run, c.binding, uuid.New())
			}
			before := onboardingSnapshot(t, tx)
			exec("SAVEPOINT down_attempt")
			_, err = tx.Exec(t.Context(), down)
			onboardingState(t, err, "BN001")
			exec("ROLLBACK TO SAVEPOINT down_attempt")
			if onboardingSnapshot(t, tx) != before {
				t.Fatal("retained Down changed complete rows, definitions or Goose versions")
			}
			if !NativeStorageSchemaReady(t.Context(), tx) {
				t.Fatal("retained Down damaged guard graph")
			}
		})
	}
}

func TestNativeOnboardingMigrationDownPermitDB(t *testing.T) {
	p := onboardingDB(t)
	_, down := onboardingMigrationSQL(t)
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Corrupt retained-state negative fixture only. Install the exact final
	// trigger definitions/enabled state before Down. This grants no publication
	// authority and proves no successful permit or completion.
	exec("ALTER TABLE bloem_native_publication_permits DISABLE TRIGGER ALL")
	exec("INSERT INTO bloem_native_publication_permits(xid,item_key,folder_key,library_revision,folder_owner_id,source_key,source_owner_id,binding_id,discovery_run_id,installation_key,runtime_generation,configuration_revision,protocol_version,ingestion_owner,ingestion_epoch,lease_token,entry_id,entry_revision,logical_path,catalog_location,entry_kind,entry_size,entry_modified_unix_nano,normalized_modified_at,parsed_container,content_group_key,file_shape,sidecar_shape) VALUES(1,'retained',1,3,$1,$1,$1,$1,$1,1,1,1,1,'retained',1,$1,'entry','r1','entry','location',1,0,0,'epoch','epub','group','{}','[]')", uuid.New())
	exec("ALTER TABLE bloem_native_publication_permits ENABLE TRIGGER ALL")
	if !NativeStorageSchemaReady(t.Context(), tx) {
		t.Fatal("negative retained permit fixture lacks exact final graph")
	}
	before := onboardingSnapshot(t, tx)
	exec("SAVEPOINT down_attempt")
	_, err = tx.Exec(t.Context(), down)
	onboardingState(t, err, "BN001")
	exec("ROLLBACK TO SAVEPOINT down_attempt")
	if onboardingSnapshot(t, tx) != before {
		t.Fatal("permit-only Down changed complete logical state")
	}
}

// Retained corruption is seeded only after a valid offline import, with the
// one structural guard temporarily disabled. Exact final guards are restored
// before classification/mutation; this is no runtime publication authority.
func TestNativeOnboardingStructuralClassificationDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	up, _ := onboardingMigrationSQL(t)
	for _, table := range []string{"media_item_roots", "media_item_groups"} {
		t.Run(table, func(t *testing.T) {
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := tx.Exec(t.Context(), q, args...); err != nil {
					t.Fatal(err)
				}
			}
			c := onboardingLegacyCandidate(t, tx, "epub")
			if _, err = tx.Exec(t.Context(), up); err != nil {
				t.Fatal(err)
			}
			class := func(key, want string) {
				t.Helper()
				var got string
				if err := tx.QueryRow(t.Context(), "SELECT bloem_native_item_class($1)", key).Scan(&got); err != nil || got != want {
					t.Fatalf("classification=%s want=%s error=%v", got, want, err)
				}
			}
			class(c.item, "native")
			var local int
			if err = tx.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Ordinary') RETURNING id").Scan(&local); err != nil {
				t.Fatal(err)
			}
			key := uuid.NewString()
			exec("INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Ordinary book')", key)
			insert := "INSERT INTO media_item_roots(media_folder_id,canonical_root_path,content_id) VALUES($1,'/ordinary/book',$2)"
			if table == "media_item_groups" {
				insert = "INSERT INTO media_item_groups(media_folder_id,group_key_version,content_group_key,content_id) VALUES($1,1,'ordinary-book',$2)"
			}
			exec(insert, local, key)
			class(key, "local")
			exec("ALTER TABLE " + table + " DISABLE TRIGGER bloem_native_" + table + "_folder")
			exec("UPDATE "+table+" SET content_id=$1 WHERE content_id=$2", c.item, key)
			exec("ALTER TABLE " + table + " ENABLE TRIGGER bloem_native_" + table + "_folder")
			if !NativeStorageSchemaReady(t.Context(), tx) {
				t.Fatal("retained corruption fixture lacks exact final graph")
			}
			class(c.item, "inconsistent")
			before := onboardingSnapshot(t, tx)
			exec("SAVEPOINT refused_mutation")
			_, err = tx.Exec(t.Context(), "UPDATE media_files SET file_size=file_size WHERE id=$1", c.files["epub"])
			onboardingState(t, err, "BN001")
			exec("ROLLBACK TO SAVEPOINT refused_mutation")
			if onboardingSnapshot(t, tx) != before {
				t.Fatal("mixed structural evidence refusal changed complete state")
			}
		})
	}
}
