//go:build integration

package resourcetenancy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/secret"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeScanFixture struct {
	pool                             *pgxpool.Pool
	store                            *Store
	repo                             *storagesource.Repository
	binding                          storagesource.Binding
	source                           storagesource.SourceConfig
	org, otherOrg, owner, otherOwner uuid.UUID
}

func nativeScanDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("SILO_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid disposable DB configuration")
	}
	template := cfg.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned database template")
	}
	adminCfg := cfg.Copy()
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminCfg)
	if err != nil {
		t.Fatal("connect clone admin")
	}
	name := "bloem_storage_test_authority_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 6
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("connect owned clone")
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	// Clone the fully migrated host template and apply missing native migrations
	// only inside this owned clone; never reset or mutate the template.
	for _, m := range []struct{ table, glob string }{
		{"bloem_storage_sources", "*_bloem_native_storage_sources.sql"},
		{"bloem_storage_installations", "*_bloem_native_storage_registry.sql"},
		{"bloem_storage_ingestion", "*_bloem_native_storage_ingestion.sql"},
	} {
		var exists *string
		if err = pool.QueryRow(t.Context(), "SELECT to_regclass($1)::text", m.table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != nil {
			continue
		}
		paths, err := filepath.Glob("../../migrations/sql/" + m.glob)
		if err != nil || len(paths) != 1 {
			t.Fatal("owned migration missing")
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			t.Fatal(err)
		}
		nativeScanSQL(t, pool, strings.Split(string(data), "-- +goose Down")[0])
	}
	return pool
}
func nativeScanSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
func newNativeScanFixture(t *testing.T, mode string) *nativeScanFixture {
	t.Helper()
	pool := nativeScanDatabase(t)
	x := &nativeScanFixture{pool: pool, store: NewStore(pool), repo: storagesource.NewRepository(pool)}
	var user int
	if err := pool.QueryRow(t.Context(), "INSERT INTO users(username,email,password_hash,role) VALUES('native-scan','native-scan@example.test','x','admin') RETURNING id").Scan(&user); err != nil {
		t.Fatal(err)
	}
	for i, pair := range []struct{ org, owner *uuid.UUID }{{&x.org, &x.owner}, {&x.otherOrg, &x.otherOwner}} {
		if err := pool.QueryRow(t.Context(), "INSERT INTO organizations(slug,name,status,owner_account_id) VALUES($1,$1,'active',$2) RETURNING id", "native-scan-"+string(rune('a'+i)), user).Scan(pair.org); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(t.Context(), "SELECT id FROM resource_owners WHERE organization_id=$1", *pair.org).Scan(pair.owner); err != nil {
			t.Fatal(err)
		}
	}
	var platform uuid.UUID
	if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&platform); err != nil {
		t.Fatal(err)
	}
	folderOwner, sourceOwner := platform, platform
	switch mode {
	case "same-org":
		folderOwner, sourceOwner = x.owner, x.owner
	case "entitled":
		folderOwner = x.owner
	case "foreign":
		folderOwner, sourceOwner = x.owner, x.otherOwner
	case "platform-foreign":
		sourceOwner = x.owner
	}
	var folder int
	if err := pool.QueryRow(t.Context(), "INSERT INTO media_folders(type,name,owner_id) VALUES('ebooks','Native authority',$1) RETURNING id", folderOwner).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	var installation int64
	if err := pool.QueryRow(t.Context(), "INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES('authority-fixture','1','/synthetic',$1) RETURNING id", sourceOwner).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	nativeScanSQL(t, pool, "INSERT INTO bloem_storage_installations(installation_id,owner_id) VALUES($1,$2)", installation, sourceOwner)
	var err error
	x.source, err = x.repo.CreateSource(t.Context(), storagesource.SourceConfig{OwnerID: sourceOwner, InstallationID: &installation, PluginID: "authority-fixture", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	x.binding, err = x.repo.Bind(t.Context(), x.source.Key, folder)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "entitled" {
		nativeScanSQL(t, pool, "INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','native-test')", x.org, platform, installation)
	}
	return x
}
func (x *nativeScanFixture) claim(t *testing.T) storagesource.IngestionClaim {
	t.Helper()
	ctx := t.Context()
	l, err := x.repo.Begin(ctx, x.source.Key, "authority-discovery", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := x.repo.NextDirectory(ctx, l)
	if err != nil || !ok {
		t.Fatalf("directory: %v", err)
	}
	entry := &storagev1.Entry{Id: "book", Name: "book.epub", LogicalPath: "Books/book.epub", Revision: "v1", Size: 12, Kind: storagev1.EntryKind_ENTRY_KIND_FILE}
	if err = x.repo.ApplyPage(ctx, l, cp, &storagev1.ListResponse{Entries: []*storagev1.Entry{entry}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err = x.repo.Complete(ctx, l); err != nil {
		t.Fatal(err)
	}
	lease, err := x.repo.BeginIngestion(ctx, l.RunID, x.binding.ID, "authority-ingest", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := x.repo.NextIngestion(ctx, lease)
	if err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	return claim
}
func TestNativeScanAuthorityOwnerPolicy(t *testing.T) {
	for _, mode := range []string{"platform", "same-org", "entitled", "foreign", "platform-foreign"} {
		t.Run(mode, func(t *testing.T) {
			x := newNativeScanFixture(t, mode)
			err := x.store.RequireNativeScan(t.Context(), x.binding, x.source)
			allowed := mode == "platform" || mode == "same-org" || mode == "entitled"
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed && !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("foreign resource authorized: %v", err)
			}
		})
	}
}
func TestNativeScanAuthorityRejectsActualAndRetainedDrift(t *testing.T) {
	for _, mode := range []string{"missing-binding", "binding-folder", "binding-source", "expected-owner", "expected-installation", "expected-config", "expected-plugin", "expected-provider", "expected-root", "expected-disabled", "source-owner", "source-plugin", "source-installation", "source-config", "source-provider", "source-root", "source-disabled", "installation-disabled", "installation-kind", "native-mark", "folder-disabled", "folder-type", "paths", "org-suspended", "grant-missing", "grant-suspended", "grant-revoked", "grant-foreign"} {
		t.Run(mode, func(t *testing.T) {
			x := newNativeScanFixture(t, "entitled")
			b, s := x.binding, x.source
			switch mode {
			case "missing-binding":
				b.ID = uuid.New()
			case "binding-folder":
				b.FolderID++
			case "binding-source":
				b.SourceKey = uuid.New()
			case "expected-owner":
				s.OwnerID = x.owner
			case "expected-installation":
				id := *s.InstallationID + 100
				s.InstallationID = &id
			case "expected-config":
				s.ConfigurationRevision++
			case "expected-plugin":
				s.PluginID = "other"
			case "expected-provider":
				s.ProviderSourceID = "other"
			case "expected-root":
				s.RootEntryID = "other"
			case "expected-disabled":
				s.Enabled = false
			case "source-owner":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET installation_id=NULL,owner_id=$2 WHERE key=$1", s.Key, x.owner)
			case "source-plugin":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET plugin_id='changed' WHERE key=$1", s.Key)
			case "source-installation":
				var second int64
				if err := x.pool.QueryRow(t.Context(), "INSERT INTO plugin_installations(plugin_id,version,install_path,owner_id) VALUES($1,'1','/synthetic/second',$2) RETURNING id", s.PluginID, s.OwnerID).Scan(&second); err != nil {
					t.Fatal(err)
				}
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET installation_id=$2 WHERE key=$1", s.Key, second)
			case "source-config":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", s.Key)
			case "source-provider":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET provider_source_id='other' WHERE key=$1", s.Key)
			case "source-root":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET root_entry_id='other' WHERE key=$1", s.Key)
			case "source-disabled":
				nativeScanSQL(t, x.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", s.Key)
			case "installation-disabled":
				nativeScanSQL(t, x.pool, "UPDATE plugin_installations SET enabled=false WHERE id=$1", s.InstallationID)
			case "installation-kind":
				nativeScanSQL(t, x.pool, "UPDATE plugin_installations SET kind='builtin' WHERE id=$1", s.InstallationID)
			case "native-mark":
				nativeScanSQL(t, x.pool, "DELETE FROM bloem_storage_installations WHERE installation_id=$1", s.InstallationID)
			case "folder-disabled":
				nativeScanSQL(t, x.pool, "UPDATE media_folders SET enabled=false WHERE id=$1", b.FolderID)
			case "folder-type":
				nativeScanSQL(t, x.pool, "UPDATE media_folders SET type='movies' WHERE id=$1", b.FolderID)
			case "paths":
				nativeScanSQL(t, x.pool, "INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'/synthetic/books')", b.FolderID)
			case "org-suspended":
				nativeScanSQL(t, x.pool, "UPDATE organizations SET status='suspended' WHERE id=$1", x.org)
			case "grant-missing":
				nativeScanSQL(t, x.pool, "DELETE FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, s.InstallationID)
			case "grant-suspended":
				nativeScanSQL(t, x.pool, "UPDATE organization_entitlements SET status='suspended' WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, s.InstallationID)
			case "grant-revoked":
				nativeScanSQL(t, x.pool, "UPDATE organization_entitlements SET status='revoked',revoked_at=now() WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, s.InstallationID)
			case "grant-foreign":
				nativeScanSQL(t, x.pool, "UPDATE organization_entitlements SET organization_id=$3 WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, s.InstallationID, x.otherOrg)
			}
			if err := x.store.RequireNativeScan(t.Context(), b, s); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("drift allowed: %v", err)
			}
		})
	}
}
func nativeScanWaitBlocked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, blocker uint32) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)))", int(blocker)).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("mutation never reached SQL barrier")
		case <-ticker.C:
		}
	}
}
func TestNativeScanAuthorityLocksThroughPublication(t *testing.T) {
	for _, mode := range []string{"revocation", "suspension", "paths-only", "blank-path-update", "folder-type", "folder-disabled", "source-config", "binding-delete", "registry-uninstall", "folder-owner", "installation-owner"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "entitled"
			if mode == "installation-owner" {
				fixtureMode = "same-org"
			}
			x := newNativeScanFixture(t, fixtureMode)
			claim := x.claim(t)
			if mode == "blank-path-update" {
				nativeScanSQL(t, x.pool, "INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'  ')", x.binding.FolderID)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			ready := make(chan uint32, 1)
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			published := make(chan error, 1)
			go func() {
				published <- x.repo.PublishAuthorizedIngestion(ctx, claim, func(ctx context.Context, tx pgx.Tx) error {
					if err := x.store.RequireNativeScanTx(ctx, tx, x.binding, x.source); err != nil {
						return err
					}
					ready <- tx.Conn().PgConn().PID()
					return nil
				}, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					_, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('authorized-publication','ebook','Approved')")
					return err
				})
			}()
			var blocker uint32
			select {
			case blocker = <-ready:
			case err := <-published:
				t.Fatalf("authorization failed: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			mutated := make(chan error, 1)
			var registry *plugins.NativeStorageRegistry
			if mode == "registry-uninstall" {
				cipher, err := secret.New([]byte(strings.Repeat("x", 32)))
				if err != nil {
					t.Fatal(err)
				}
				registry, err = plugins.NewNativeStorageRegistry(x.pool, cipher, t.TempDir(), nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			go func() {
				var err error
				switch mode {
				case "revocation":
					_, err = x.pool.Exec(ctx, "UPDATE organization_entitlements SET status='revoked',revoked_at=now() WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, x.source.InstallationID)
				case "suspension":
					_, err = x.pool.Exec(ctx, "UPDATE organizations SET status='suspended' WHERE id=$1", x.org)
				case "paths-only":
					paths := []string{"/synthetic/concurrent"}
					err = catalog.NewFolderRepository(x.pool).Update(ctx, x.binding.FolderID, catalog.UpdateFolderInput{Paths: &paths})
				case "blank-path-update":
					_, err = x.pool.Exec(ctx, "UPDATE media_folder_paths SET path='/synthetic/from-blank' WHERE media_folder_id=$1", x.binding.FolderID)
				case "folder-owner":
					_, err = x.pool.Exec(ctx, "UPDATE media_folders SET owner_id=$2 WHERE id=$1", x.binding.FolderID, x.otherOwner)
				case "installation-owner":
					err = nativeScanReplaceInstallationOwner(ctx, x)
				case "folder-type":
					_, err = x.pool.Exec(ctx, "UPDATE media_folders SET type='movies' WHERE id=$1", x.binding.FolderID)
				case "folder-disabled":
					_, err = x.pool.Exec(ctx, "UPDATE media_folders SET enabled=false WHERE id=$1", x.binding.FolderID)
				case "source-config":
					_, err = x.pool.Exec(ctx, "UPDATE bloem_storage_sources SET provider_source_id='changed' WHERE key=$1", x.source.Key)
				case "binding-delete":
					_, err = x.pool.Exec(ctx, "DELETE FROM bloem_storage_bindings WHERE id=$1", x.binding.ID)
				case "registry-uninstall":
					err = registry.Uninstall(ctx, int(*x.source.InstallationID), x.source.OwnerID)
				}
				mutated <- err
			}()
			nativeScanWaitBlocked(t, ctx, x.pool, blocker)
			close(release)
			if err := <-published; err != nil {
				t.Fatalf("publication: %v", err)
			}
			if err := <-mutated; err != nil {
				t.Fatalf("mutation: %v", err)
			}
			nativeScanCheckReplacedOwner(t, ctx, x, mode)
			if err := x.store.RequireNativeScan(ctx, x.binding, x.source); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("subsequent authorization accepted mutation: %v", err)
			}
			var count int
			if err := x.pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE content_id='authorized-publication'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("publication not committed: %d %v", count, err)
			}
			var last string
			if err := x.pool.QueryRow(ctx, "SELECT last_entry_id FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", claim.Lease.RunID, claim.Lease.BindingID).Scan(&last); err != nil {
				// Binding deletion cascades its checkpoint; all other cases retain it.
				if mode != "binding-delete" {
					t.Fatal(err)
				}
			} else if last != "book" {
				t.Fatalf("checkpoint = %q", last)
			}
		})
	}
}
func TestNativeScanAuthorityConcurrentMutationWins(t *testing.T) {
	for _, mode := range []string{"revocation", "suspension", "source-config", "registry-uninstall", "folder-owner", "installation-owner"} {
		t.Run(mode, func(t *testing.T) {
			fixtureMode := "entitled"
			if mode == "installation-owner" {
				fixtureMode = "same-org"
			}
			x := newNativeScanFixture(t, fixtureMode)
			claim := x.claim(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			tx, err := x.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			var query string
			var args []any
			switch mode {
			case "revocation":
				query = "SELECT id FROM organization_entitlements WHERE organization_id=$1 AND plugin_installation_id=$2 FOR UPDATE"
				args = []any{x.org, x.source.InstallationID}
			case "suspension":
				query = "SELECT id FROM organizations WHERE id=$1 FOR UPDATE"
				args = []any{x.org}
			case "source-config":
				query = "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE"
				args = []any{x.source.Key}
			case "folder-owner":
				query = "SELECT id FROM media_folders WHERE id=$1 FOR UPDATE"
				args = []any{x.binding.FolderID}
			case "installation-owner":
				query = "SELECT id FROM plugin_installations WHERE id=$1 FOR UPDATE"
				args = []any{x.source.InstallationID}
			case "registry-uninstall":
				query = "SELECT id FROM plugin_installations WHERE id=$1 FOR NO KEY UPDATE"
				args = []any{x.source.InstallationID}
			}
			if _, err = tx.Exec(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				result <- x.repo.PublishAuthorizedIngestion(ctx, claim, func(ctx context.Context, tx pgx.Tx) error {
					return x.store.RequireNativeScanTx(ctx, tx, x.binding, x.source)
				}, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
					_, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('denied-publication','ebook','Denied')")
					return err
				})
			}()
			nativeScanWaitBlocked(t, ctx, x.pool, tx.Conn().PgConn().PID())
			switch mode {
			case "revocation":
				_, err = tx.Exec(ctx, "UPDATE organization_entitlements SET status='revoked',revoked_at=now() WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, x.source.InstallationID)
			case "suspension":
				_, err = tx.Exec(ctx, "UPDATE organizations SET status='suspended' WHERE id=$1", x.org)
			case "source-config":
				_, err = tx.Exec(ctx, "UPDATE bloem_storage_sources SET provider_source_id='changed' WHERE key=$1", x.source.Key)
			case "folder-owner":
				_, err = tx.Exec(ctx, "UPDATE media_folders SET owner_id=$2 WHERE id=$1", x.binding.FolderID, x.otherOwner)
			case "installation-owner":
				err = nativeScanReplaceInstallationOwnerTx(ctx, tx, x)
			case "registry-uninstall":
				// Exact registry installation -> source -> runs -> entitlement -> delete order.
				_, err = tx.Exec(ctx, "UPDATE bloem_storage_sources SET enabled=false,configuration_revision=configuration_revision+1 WHERE installation_id=$1", x.source.InstallationID)
				if err == nil {
					_, err = tx.Exec(ctx, "UPDATE bloem_storage_scan_runs SET state='failed',lease_epoch=lease_epoch+1 WHERE source_key IN(SELECT key FROM bloem_storage_sources WHERE installation_id=$1) AND state='running'", x.source.InstallationID)
				}
				if err == nil {
					_, err = tx.Exec(ctx, "UPDATE bloem_storage_sources SET installation_id=NULL WHERE installation_id=$1", x.source.InstallationID)
				}
				if err == nil {
					_, err = tx.Exec(ctx, "DELETE FROM organization_entitlements WHERE plugin_installation_id=$1", x.source.InstallationID)
				}
				if err == nil {
					_, err = tx.Exec(ctx, "DELETE FROM plugin_installations WHERE id=$1", x.source.InstallationID)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			nativeScanCheckReplacedOwner(t, ctx, x, mode)
			if err = <-result; !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("concurrent mutation authorized: %v", err)
			}
			var last string
			var token *uuid.UUID
			var count int
			if err = x.pool.QueryRow(ctx, "SELECT last_entry_id,pending_token,(SELECT count(*) FROM media_items WHERE content_id='denied-publication') FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", claim.Lease.RunID, claim.Lease.BindingID).Scan(&last, &token, &count); err != nil {
				t.Fatal(err)
			}
			if last != "" || token == nil || *token != claim.Token || count != 0 {
				t.Fatalf("denial changed publication/checkpoint: %q %v %d", last, token, count)
			}
		})
	}
}

// Path-only replacement locks/deletes existing paths before inserting its new
// FK row. Authority must fail fast on that child-first lock rather than wait
// while retaining the folder lock and create a folder/path deadlock.
func TestNativeScanAuthorityPendingPathReplacementFailsFast(t *testing.T) {
	x := newNativeScanFixture(t, "entitled")
	nativeScanSQL(t, x.pool, "INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,' ')", x.binding.FolderID)
	updater, err := x.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = updater.Rollback(context.Background()) }()
	if _, err = updater.Exec(t.Context(), "DELETE FROM media_folder_paths WHERE media_folder_id=$1", x.binding.FolderID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	err = x.store.RequireNativeScan(ctx, x.binding, x.source)
	var pgErr *pgconn.PgError
	if !errors.Is(err, ErrResourceUnavailable) || !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("pending child-first replacement must fail with lock_not_available: %v", err)
	}
	if _, err = updater.Exec(t.Context(), "INSERT INTO media_folder_paths(media_folder_id,path) VALUES($1,'/synthetic/replacement')", x.binding.FolderID); err != nil {
		t.Fatal(err)
	}
	if err = updater.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = x.store.RequireNativeScan(t.Context(), x.binding, x.source); !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("replacement path authorized: %v", err)
	}
}

// Exercise the actual SQL folder type at both pre-I/O and publication authority.
func TestNativeScanAuthorityEbookAliases(t *testing.T) {
	for _, tc := range []struct {
		name, kind       string
		enabled, allowed bool
	}{
		{"plural", "ebooks", true, true},
		{"singular", "ebook", true, true},
		{"trimmed-singular", "  ebook\t", true, true},
		{"case-singular", "EBOOK", true, true},
		{"normalized-plural", " \tEbOoKs\n", true, true},
		{"invalid", "movies", true, false},
		{"empty", "  ", true, false},
		{"near-alias", "ebookz", true, false},
		{"disabled-alias", " EBOOK ", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newNativeScanFixture(t, "entitled")
			claim := x.claim(t)
			nativeScanSQL(t, x.pool, "UPDATE media_folders SET type=$2,enabled=$3 WHERE id=$1", x.binding.FolderID, tc.kind, tc.enabled)
			check := func(where string, err error) {
				t.Helper()
				if tc.allowed && err != nil {
					t.Errorf("%s denied valid alias %q: %v", where, tc.kind, err)
				}
				if !tc.allowed && !errors.Is(err, ErrResourceHidden) {
					t.Errorf("%s allowed invalid/disabled kind %q: %v", where, tc.kind, err)
				}
			}
			check("pre-I/O", x.store.RequireNativeScan(t.Context(), x.binding, x.source))
			called := false
			err := x.repo.PublishAuthorizedIngestion(t.Context(), claim, func(ctx context.Context, tx pgx.Tx) error {
				return x.store.RequireNativeScanTx(ctx, tx, x.binding, x.source)
			}, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
				called = true
				_, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('alias-publication','ebook','Alias')")
				return err
			})
			check("publication", err)
			var last string
			var token *uuid.UUID
			var count int
			if err := x.pool.QueryRow(t.Context(), "SELECT last_entry_id,pending_token,(SELECT count(*) FROM media_items WHERE content_id='alias-publication') FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", claim.Lease.RunID, claim.Lease.BindingID).Scan(&last, &token, &count); err != nil {
				t.Fatal(err)
			}
			if tc.allowed {
				if !called || count != 1 || last != "book" || token != nil {
					t.Fatalf("allowed publication/checkpoint: %v %d %q %v", called, count, last, token)
				}
			} else if called || count != 0 || last != "" || token == nil || *token != claim.Token {
				t.Fatalf("denial changed publication/checkpoint: %v %d %q %v", called, count, last, token)
			}
		})
	}
}

// Respect installation -> source order while removing composite FK references
// before replacing the actual installation owner. No shared schema is changed.
func nativeScanReplaceInstallationOwner(ctx context.Context, x *nativeScanFixture) error {
	tx, err := x.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM plugin_installations WHERE id=$1 FOR UPDATE", x.source.InstallationID); err != nil {
		return err
	}
	if err = nativeScanReplaceInstallationOwnerTx(ctx, tx, x); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func nativeScanReplaceInstallationOwnerTx(ctx context.Context, tx pgx.Tx, x *nativeScanFixture) error {
	if _, err := tx.Exec(ctx, "UPDATE bloem_storage_sources SET installation_id=NULL WHERE key=$1", x.source.Key); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM bloem_storage_installations WHERE installation_id=$1", x.source.InstallationID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE plugin_installations SET owner_id=$2 WHERE id=$1", x.source.InstallationID, x.otherOwner)
	return err
}

func TestNativeScanAuthorityRejectsMultipleBindings(t *testing.T) {
	x := newNativeScanFixture(t, "entitled")
	claim := x.claim(t)
	installation := *x.source.InstallationID
	second, err := x.repo.CreateSource(t.Context(), storagesource.SourceConfig{
		OwnerID: x.source.OwnerID, InstallationID: &installation, PluginID: x.source.PluginID,
		ProviderSourceID: "second-books", RootEntryID: "root", ConfigurationRevision: 1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Malformed durable binding state is legal in the schema, but not scan policy.
	nativeScanSQL(t, x.pool, "INSERT INTO bloem_storage_bindings(id,source_key,folder_id) VALUES($1,$2,$3)", uuid.New(), second.Key, x.binding.FolderID)
	if err = x.store.RequireNativeScan(t.Context(), x.binding, x.source); !errors.Is(err, ErrResourceHidden) {

		t.Fatalf("multiple bindings authorized: %v", err)
	}
	err = x.repo.PublishAuthorizedIngestion(t.Context(), claim, func(ctx context.Context, tx pgx.Tx) error {
		return x.store.RequireNativeScanTx(ctx, tx, x.binding, x.source)
	}, func(context.Context, pgx.Tx, *storagev1.Entry) error {
		t.Error("malformed binding reached publisher")
		return nil
	})
	if !errors.Is(err, ErrResourceHidden) {
		t.Fatalf("multiple bindings published: %v", err)
	}
	var last string
	var token *uuid.UUID
	if err := x.pool.QueryRow(t.Context(), "SELECT last_entry_id,pending_token FROM bloem_storage_ingestion WHERE run_id=$1 AND binding_id=$2", claim.Lease.RunID, claim.Lease.BindingID).Scan(&last, &token); err != nil {
		t.Fatal(err)
	}
	if last != "" || token == nil || *token != claim.Token {
		t.Fatalf("binding denial changed checkpoint: %q %v", last, token)
	}
}

func nativeScanCheckReplacedOwner(t *testing.T, ctx context.Context, x *nativeScanFixture, mode string) {
	t.Helper()
	var query string
	var id any
	switch mode {
	case "folder-owner":
		query, id = "SELECT owner_id FROM media_folders WHERE id=$1", x.binding.FolderID
	case "installation-owner":
		query, id = "SELECT owner_id FROM plugin_installations WHERE id=$1", x.source.InstallationID
	default:
		return
	}
	var owner uuid.UUID
	if err := x.pool.QueryRow(ctx, query, id).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != x.otherOwner {
		t.Fatalf("owner replacement did not commit: %s", owner)
	}
}
