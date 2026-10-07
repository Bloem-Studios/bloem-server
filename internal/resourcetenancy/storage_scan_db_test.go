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

	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type nativeScanFixture struct {
	pool                             *pgxpool.Pool
	store                            *Store
	repo                             *storagesource.Repository
	binding                          storagesource.Location
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
	x.binding = storagesource.Location{ID: uuid.New(), SourceKey: x.source.Key, FolderID: folder}
	nativeScanSQL(t, pool, "INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)", x.binding.ID, x.binding.SourceKey, x.binding.FolderID)
	if mode == "entitled" {
		nativeScanSQL(t, pool, "INSERT INTO organization_entitlements(organization_id,entitlement_kind,root_kind,root_owner_id,plugin_installation_id,status,granted_by_service) VALUES($1,'plugin_availability','plugin_installation',$2,$3,'active','native-test')", x.org, platform, installation)
	}
	return x
}
func TestNativeScanAuthorityOwnerPolicy(t *testing.T) {
	for _, mode := range []string{"platform", "same-org", "entitled", "foreign", "platform-foreign"} {
		t.Run(mode, func(t *testing.T) {
			x := newNativeScanFixture(t, mode)
			err := x.store.RequireStorageScan(t.Context(), x.binding, x.source)
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
			if err := x.store.RequireStorageScan(t.Context(), b, s); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("drift allowed: %v", err)
			}
		})
	}
}
