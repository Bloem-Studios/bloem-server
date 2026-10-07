//go:build integration

package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloemtestdb"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/Silo-Server/silo-server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func onboardingDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("SILO_TEST_DATABASE_URL") != "" {
		t.Fatal("SILO_TEST_DATABASE_URL must be unset")
	}
	private := "../../.superpowers/sdd/2026-10-06-native-storage-persistence/database-url"
	st, err := os.Stat(private)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private mode-0600 fixture required")
	}
	b, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("read private fixture")
	}
	template, err := pgxpool.ParseConfig(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal("parse private template identity")
	}
	dsn, cleanup, err := bloemtestdb.CloneNativeOnboarding(t.Context(), strings.TrimSpace(string(b)), false)
	if err != nil {
		t.Fatal(err)
	}
	var p *pgxpool.Pool
	t.Cleanup(func() {
		if p != nil {
			p.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := cleanup(ctx); err != nil {
			t.Error(err)
		} else {
			t.Log("private UUID clone cleanup verified")
		}
	})
	cfg, err := bloemtestdb.NativeOnboardingPoolConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	p, err = pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("open private clone")
	}
	t.Logf("owned private UUID clone: %s", p.Config().ConnConfig.Database)
	var actual string
	if err = p.QueryRow(t.Context(), "SELECT current_database()").Scan(&actual); err != nil ||
		actual == template.ConnConfig.Database || actual != p.Config().ConnConfig.Database || !strings.HasPrefix(actual, "bloem_storage_test_acore_") {
		t.Fatal("refusing fixture SQL outside the owned UUID clone")
	}
	if err := bloemtestdb.PrepareNativeOnboardingPool(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return p
}
func onboardingExec(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := p.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func onboardingState(t *testing.T, err error, code string) {
	t.Helper()
	var e *pgconn.PgError
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("expected SQLSTATE %s, actual class=%T", code, err)
	}
}

// The runtime refuses trusted Bind into an ordinary folder. Root's separately
// recorded actual PRE-MODE publication is the retained-file behavioral RED.
func TestNativeOnboardingUnmarkedBindingGuardDB(t *testing.T) {
	p := onboardingDB(t)
	src, err := storagesource.NewRepository(p).CreateSource(t.Context(), storagesource.SourceConfig{
		PluginID: "onboarding-negative", ProviderSourceID: uuid.NewString(), RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	var folder int
	if err = p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebook','Guard negative',$1) RETURNING id", src.OwnerID).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	_, err = storagesource.NewRepository(p).Bind(t.Context(), src.Key, folder)
	onboardingState(t, err, "BN001")
	var count int
	if err = p.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_bindings WHERE folder_id=$1", folder).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused binding changed durable state")
	}
	onboardingExec(t, p, "UPDATE media_folders SET name=name WHERE id=$1", folder)
}
func TestNativeOnboardingCreateDB(t *testing.T) {
	p := onboardingDB(t)
	var owner uuid.UUID
	if err := p.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	r := NewFolderRepository(p)
	key := uuid.New()
	got, err := r.CreateNativeEbookTx(t.Context(), tx, NativeLibraryCreate{OwnerID: owner, CreationKey: key, Name: " Books "})
	if err != nil {
		t.Fatal("native creation failed", err)
	}
	if got.LibraryID <= 0 || got.OwnerID != owner || got.CreationKey != key || got.Initialized || got.Revision != 1 || got.DeletingJobID != nil {
		t.Fatal("creation state incorrect")
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	var paths, groups int
	if err = p.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM media_folder_paths WHERE media_folder_id=$1),(SELECT count(*) FROM library_collection_groups WHERE library_id=$1 AND id=$2)", got.LibraryID, CanonicalUserCollectionsGroupID(got.LibraryID)).Scan(&paths, &groups); err != nil || paths != 0 || groups != 1 {
		t.Fatal("atomic pathless creation/group witness absent")
	}
	_, err = NewNativeLocalFolderReader(r).GetByID(t.Context(), got.LibraryID)
	var typed *NativeOnboardingError
	if !errors.As(err, &typed) || typed.Code != "native_local_operation_unsupported" {
		t.Fatal("local reader did not refuse native mode")
	}
	for _, q := range []string{"UPDATE media_folders SET name=name WHERE id=$1", "UPDATE media_folders SET allow_empty_cleanup_once=false WHERE id=$1", "DELETE FROM media_folders WHERE id=$1"} {
		_, err = p.Exec(t.Context(), q, got.LibraryID)
		onboardingState(t, err, "BN001")
	}
	onboardingExec(t, p, "UPDATE media_folders SET last_scanned_at=now(),scan_warning_code=NULL WHERE id=$1", got.LibraryID)
}

func onboardingNativeL1(t *testing.T, p *pgxpool.Pool) NativeLibraryState {
	t.Helper()
	var owner uuid.UUID
	if err := p.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	state, err := NewFolderRepository(p).CreateNativeEbookTx(t.Context(), tx, NativeLibraryCreate{OwnerID: owner, CreationKey: uuid.New(), Name: "Native L1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return state
}
func TestNativeOnboardingInventoryDB(t *testing.T) {
	p := onboardingDB(t)
	if !NativeStorageSchemaReady(t.Context(), p) {
		t.Fatal("exact installed inventory unavailable")
	}
	for _, q := range []string{
		"ALTER TABLE media_files DISABLE TRIGGER bloem_native_media_files_guard",
		"DROP TRIGGER bloem_native_media_extras_book ON media_extras",
		"DROP TRIGGER bloem_native_user_dropped_series_book ON user_dropped_series",
		"DROP INDEX bloem_native_binding_folder_unique",
		"DROP INDEX idx_media_item_roots_content_id",
		"DROP INDEX idx_media_item_groups_content_id",
		"DROP INDEX idx_media_files_extra_id; CREATE INDEX idx_media_files_extra_id ON media_files(extra_id) WHERE media_folder_id=-1",
		"ALTER TABLE media_folders ADD COLUMN onboarding_future_unclassified text",
		`CREATE SCHEMA onboarding_alternate;
CREATE FUNCTION onboarding_alternate.bloem_native_guard_file() RETURNS trigger LANGUAGE plpgsql VOLATILE AS 'BEGIN RETURN NEW; END';
DROP TRIGGER bloem_native_media_files_guard ON public.media_files;
CREATE TRIGGER bloem_native_media_files_guard BEFORE INSERT OR UPDATE OR DELETE ON public.media_files FOR EACH ROW EXECUTE FUNCTION onboarding_alternate.bloem_native_guard_file()`,

		"CREATE OR REPLACE FUNCTION public.bloem_native_lock_item_keys(supplied text[]) RETURNS void LANGUAGE plpgsql VOLATILE AS 'BEGIN RETURN; END'",
	} {
		tx, err := p.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), q); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatal(err)
		}
		if NativeStorageSchemaReady(t.Context(), tx) {
			_ = tx.Rollback(context.Background())
			t.Fatalf("inventory accepted changed definition: %s", q)
		}
		if err = tx.Rollback(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !NativeStorageSchemaReady(t.Context(), p) {
			t.Fatal("rollback did not restore readiness for substituted definition")
		}
	}
	if !NativeStorageSchemaReady(t.Context(), p) {
		t.Fatal("rolled-back drift persisted")
	}
}
func TestNativeOnboardingLocalControlsDB(t *testing.T) {
	p := onboardingDB(t)
	state := onboardingNativeL1(t, p)
	var local int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&local); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	onboardingExec(t, p, "INSERT INTO media_items(content_id,type,title) VALUES($1,'ebook','Local')", key)
	for _, iso := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead, pgx.Serializable} {
		tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
		defer func() {
			if tx != nil {
				_ = tx.Rollback(context.Background())
			}
		}()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), "UPDATE media_items SET title=title WHERE content_id=$1", key); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(t.Context(), "UPDATE media_folders SET name=name WHERE id=$1", local); err != nil {
			t.Fatal(err)
		}
		if err = NewFolderRepository(p).requireLocalLibraryMutation(t.Context(), tx, local); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	folder, native, err := NewFolderRepository(p).NativeScanFolder(t.Context(), local)
	if err != nil || native || folder.ID != local {
		t.Fatal("ordinary reader mode changed")
	}
	_, native, err = NewFolderRepository(p).NativeScanFolder(t.Context(), state.LibraryID)
	var typed *NativeOnboardingError
	if !native || !errors.As(err, &typed) || typed.Code != "native_library_not_initialized" {
		t.Fatal("uninitialized native mode was lost")
	}
	if err = NewFolderRepository(p).requireLocalLibraryDelete(t.Context(), state.LibraryID); !errors.As(err, &typed) || typed.Code != "native_library_delete_unsupported" {
		t.Fatal("native delete classification")
	}
}
func TestNativeOnboardingNegativeAdmissionDB(t *testing.T) {
	p := onboardingDB(t)
	state := onboardingNativeL1(t, p)
	key := uuid.NewString()
	var local int
	if err := p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name) VALUES('ebook','Local') RETURNING id").Scan(&local); err != nil {
		t.Fatal(err)
	}
	for _, iso := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		for _, q := range []string{
			"INSERT INTO media_files(file_path,media_folder_id,content_id) VALUES($1,$2,$3)",
			"INSERT INTO user_dropped_series(user_id,profile_id,series_id) VALUES(1,'unused',$3)",
		} {
			tx, err := p.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: iso})
			defer func() {
				if tx != nil {
					_ = tx.Rollback(context.Background())
				}
			}()
			if err != nil {
				t.Fatal(err)
			}
			var absent bool
			if err = tx.QueryRow(t.Context(), "SELECT NOT EXISTS(SELECT 1 FROM media_items WHERE content_id=$1)", key).Scan(&absent); err != nil || !absent {
				t.Fatal("absence snapshot")
			}
			if strings.Contains(q, "user_dropped_series") {
				_, err = tx.Exec(t.Context(), "INSERT INTO user_dropped_series(user_id,profile_id,series_id) VALUES(1,'unused',$1)", key)
			} else {
				_, err = tx.Exec(t.Context(), q, "local-"+uuid.NewString(), local, key)
			}
			onboardingState(t, err, "BN003")
			if err = tx.Commit(t.Context()); err == nil {
				t.Fatal("refused admission committed")
			}
		}
	}
	for _, q := range []string{
		"INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'')",
		"INSERT INTO media_files(file_path,media_folder_id) VALUES('refused',$1)",
		"UPDATE bloem_native_libraries SET initialized=true,revision=2 WHERE folder_id=$1",
		"UPDATE bloem_native_libraries SET revision=3 WHERE folder_id=$1",
		"DELETE FROM bloem_native_libraries WHERE folder_id=$1",
	} {
		_, err := p.Exec(t.Context(), q, state.LibraryID)
		onboardingState(t, err, "BN001")
	}
}
func TestNativeOnboardingPublicationWithoutPermitDB(t *testing.T) {
	p := onboardingDB(t)
	state := onboardingNativeL1(t, p)
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = BeginNativePublicationPermitTx(t.Context(), tx, NativePublicationInput{FolderID: state.LibraryID}); err == nil {
		t.Fatal("incomplete input granted authority")
	}
	if err = RecordNativePublicationFileTx(t.Context(), tx, 1); err == nil {
		t.Fatal("Record created authority")
	}
	if _, err = NativePublicationStoredFileTx(t.Context(), tx); err == nil {
		t.Fatal("stored ID without permit")
	}
	// QueryRow ErrNoRows does not abort this transaction.
	if err = FinishNativePublicationPermitTx(t.Context(), tx, 1); err == nil {
		t.Fatal("Finish without permit")
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func onboardingMigrationSQL(t *testing.T) (string, string) {
	t.Helper()
	b, err := fs.ReadFile(migrations.FS, nativeOnboardingMigration)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(b), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration boundary")
	}
	return parts[0], parts[1]
}
func onboardingMigrationProvider(t *testing.T, p *pgxpool.Pool) *goose.Provider {
	t.Helper()
	body, err := fs.ReadFile(migrations.FS, nativeOnboardingMigration)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDBFromPool(p)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{
		"20261006145307_bloem_native_storage_onboarding.sql": &fstest.MapFile{Data: body},
	}, goose.WithTableName("public.goose_native_onboarding_case"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if _, err = provider.GetDBVersion(t.Context()); err != nil {
		t.Fatal(err)
	}
	return provider
}
func onboardingSnapshot(t *testing.T, p nativeModeQueryer) [32]byte {
	t.Helper()
	var buf bytes.Buffer
	rows, err := p.Query(t.Context(), "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, s)
	}
	rows.Close()
	for _, table := range tables {
		var data []byte
		if err = p.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) FROM "+pgx.Identifier{"public", table}.Sanitize()+" t").Scan(&data); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&buf, table, string(data))
	}
	for _, q := range []string{
		`SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]') FROM (
 SELECT jsonb_build_array(c.relname,t.tgname,pg_get_triggerdef(t.oid),t.tgenabled,t.tgdeferrable,t.tginitdeferred) v
 FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public') x`,
		`SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]') FROM (
 SELECT jsonb_build_array(p.proname,pg_get_functiondef(p.oid)) v FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='public' AND p.prokind='f') x`,
		`SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]') FROM (SELECT jsonb_build_array(tablename,indexname,indexdef) v FROM pg_indexes WHERE schemaname='public') x`,
		`SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]') FROM (
 SELECT jsonb_build_array(table_name,column_name,data_type,is_nullable,column_default) v FROM information_schema.columns WHERE table_schema='public') x`,
	} {
		var data []byte
		if err = p.QueryRow(t.Context(), q).Scan(&data); err != nil {
			t.Fatal(err)
		}
		buf.Write(data)
	}
	return sha256.Sum256(buf.Bytes())
}
func onboardingDownCurrent(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	_, down := onboardingMigrationSQL(t)
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestNativeOnboardingMigrationEmptyDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	provider := onboardingMigrationProvider(t, p)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !NativeStorageSchemaReady(t.Context(), p) {
		t.Fatal("Goose Up did not install exact graph")
	}
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := p.QueryRow(t.Context(), "SELECT count(*) FROM pg_proc WHERE proname LIKE 'bloem_native_%'").Scan(&count); err != nil || count != 0 {
		t.Fatal("owned functions retained after empty Down")
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !NativeStorageSchemaReady(t.Context(), p) {
		t.Fatal("empty roundtrip graph differs")
	}
}
func TestNativeOnboardingMigrationDownRetentionDB(t *testing.T) {
	// Every case is an independent clone, so an earlier retained category cannot
	// mask the category under test. Expired/completed claims remain evidence.
	cases := []string{"marker", "source", "installation"}
	for _, category := range cases {
		t.Run(category, func(t *testing.T) {
			p := onboardingDB(t)
			_, down := onboardingMigrationSQL(t)
			// Seed lower-layer categories only after temporarily removing the exact
			// final triggers in the disposable clone. Reinstall exact bodies afterwards.
			switch category {
			case "marker":
				onboardingNativeL1(t, p)
			case "source":
				_, err := storagesource.NewRepository(p).CreateSource(t.Context(), storagesource.SourceConfig{PluginID: "retained", ProviderSourceID: uuid.NewString(), RootEntryID: "root", ConfigurationRevision: 1})
				if err != nil {
					t.Fatal(err)
				}
			case "installation":
				var id int
				if err := p.QueryRow(t.Context(), "INSERT INTO plugin_installations(plugin_id,version,install_path) VALUES('retained','1','fixture') RETURNING id").Scan(&id); err != nil {
					t.Fatal(err)
				}
				onboardingExec(t, p, "INSERT INTO bloem_storage_installations(installation_id,owner_id) SELECT id,owner_id FROM plugin_installations WHERE id=$1", id)
			}
			before := onboardingSnapshot(t, p)
			tx, err := p.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), down)
			onboardingState(t, err, "BN001")
			if err = tx.Commit(t.Context()); err == nil {
				t.Fatal("retained Down committed")
			}
			if after := onboardingSnapshot(t, p); after != before {
				t.Fatal("failed Down changed rows, versions or objects")
			}
		})
	}
}
func TestNativeOnboardingMigrationUpRefusalDB(t *testing.T) {
	p := onboardingDB(t)
	onboardingDownCurrent(t, p)
	provider := onboardingMigrationProvider(t, p)
	src, err := storagesource.NewRepository(p).CreateSource(t.Context(), storagesource.SourceConfig{PluginID: "legacy", ProviderSourceID: uuid.NewString(), RootEntryID: "root", ConfigurationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	var folder int
	if err = p.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebook','Unwitnessed legacy',$1) RETURNING id", src.OwnerID).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if _, err = storagesource.NewRepository(p).Bind(t.Context(), src.Key, folder); err != nil {
		t.Fatal(err)
	}
	before := onboardingSnapshot(t, p)
	if _, err = provider.Up(t.Context()); err == nil {
		t.Fatal("ambiguous legacy import succeeded")
	}
	if onboardingSnapshot(t, p) != before {
		t.Fatal("failed Up changed complete logical state or Goose versions")
	}
}
func TestNativeOnboardingSQLInventory(t *testing.T) {
	triggers, functions, ok := nativeSchemaContracts()
	if !ok {
		t.Fatal("finite inventory missing")
	}
	wantTables := strings.Fields("media_folders media_folder_paths bloem_native_libraries bloem_storage_bindings media_items media_files media_item_libraries bloem_storage_file_refs admin_playback_history user_downloads downloads user_watch_history user_watch_progress user_favorites user_watchlist user_ratings user_personal_collection_items library_collection_items user_home_item_dismissals user_history_hidden_items user_audio_preferences user_subtitle_preferences user_series_playback_preferences user_dropped_series watch_provider_rating_items watch_provider_dropped_items ebook_reader_progress media_item_provider_ids seasons episodes episode_libraries media_item_roots media_item_groups scanned_media_roots scanned_media_groups media_group_locations observed_media_locations media_group_overrides skipped_media_roots media_root_overrides series_root_match_queue bloem_native_publication_permits media_extras")
	seen := map[string]bool{}
	for _, tr := range triggers {
		seen[tr.table] = true
	}
	var tables []string
	for table := range seen {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	sort.Strings(wantTables)
	if strings.Join(tables, ",") != strings.Join(wantTables, ",") {
		t.Fatal("finite table graph differs")
	}
	for _, name := range strings.Fields("folder_class item_class lock_item_keys guard_identity guard_folder_protected guard_folder_changed guard_folder_child guard_marker guard_binding guard_item guard_file guard_member guard_ref guard_video guard_permit check_association_complete check_publication_complete guard_book_drop guard_extra") {
		if _, ok := functions["bloem_native_"+name]; !ok {
			t.Fatalf("missing required function %s", name)
		}
	}
}

func TestNativeOnboardingLookupPlansDB(t *testing.T) {
	p := onboardingDB(t)
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(t.Context(), "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"SELECT id FROM media_files WHERE file_path='lookup' FOR NO KEY UPDATE",
		"SELECT * FROM media_files WHERE id=1",
		"SELECT * FROM media_items WHERE content_id='lookup'",
		"SELECT * FROM media_files WHERE content_id='lookup' OR episode_id='lookup' OR extra_id='lookup'",
		"SELECT * FROM media_item_libraries WHERE content_id='lookup' AND media_folder_id=1",
		"SELECT * FROM media_item_roots WHERE content_id='lookup'",
		"SELECT * FROM media_item_groups WHERE content_id='lookup'",
		"SELECT * FROM bloem_storage_file_refs WHERE media_file_id=1",
		"SELECT * FROM bloem_storage_file_refs WHERE binding_id='00000000-0000-0000-0000-000000000001' AND entry_id='lookup'",
		"SELECT * FROM bloem_storage_entries WHERE source_key='00000000-0000-0000-0000-000000000001' AND entry_id='lookup'",
		"SELECT * FROM user_dropped_series WHERE series_id='lookup'",
		"SELECT * FROM watch_provider_dropped_items WHERE series_id='lookup'",
		"SELECT * FROM media_extras WHERE content_id='lookup' OR parent_id='lookup'",
	} {
		rows, err := tx.Query(t.Context(), "EXPLAIN "+q)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line)
			plan.WriteByte('\n')
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), "Index") || strings.Contains(plan.String(), "Seq Scan") {
			t.Fatal("required keyed lookup has no usable index", q, plan.String())
		}
	}
	// enable_seqscan=off proves eligibility on the empty fixture; it is not a
	// throughput or production planner-preference claim.
}
