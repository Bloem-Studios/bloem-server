//go:build integration

package api

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/blobstore"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/imagecache"
	"github.com/Silo-Server/silo-server/internal/libraryingest"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/scanqueue"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNativeOnboardingHTTPCapabilityCompositionDB(t *testing.T) {
	runNativeOnboardingHTTPComponent(t, "success", func(t *testing.T, f nativeOnboardingLifecycleFixture) {
		// The original fixture's incomplete-dependency false assertions run first.
		// Rebuild a complete actual composition without replacing any authority.
		d := f.support.deps
		publisher := scanner.NewScanner(scanner.NewFileRepository(f.pool), "", nil, 1, false, 0)
		assets, err := blobstore.NewFilesystem(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		cacher := imagecache.New(assets)
		cacher.SetArtworkRevisionTracker(catalog.NewArtworkRevisionTracker(f.pool))
		publisher.SetImageCacher(cacher)
		consumer, err := libraryingest.NewNativeConsumer(f.host, storagesource.NewRepository(f.pool), resourcetenancy.NewStore(f.pool), publisher)
		if err != nil {
			t.Fatal(err)
		}
		executor := libraryingest.NewExecutor(publisher, nil, d.FolderRepo, nil, nil, nil)
		executor.SetNativeIngestor(consumer)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		queue := scanqueue.NewService(scanqueue.NewRepository(f.pool), d.FolderRepo, executor, nil, ctx, 1, 1)
		defer queue.Stop()
		d.Scanner, d.LibraryIngester, d.LibraryScanQueue = publisher, executor, queue
		d.NativeStorageManagement = nil
		d = nativeStorageOnboardingDependencies(d)
		f.support.rebuild(d, nil)
		before, err := os.ReadFile(f.journal)
		if err != nil {
			t.Fatal("read provider receipt before capability check")
		}
		check := func(ready bool) {
			t.Helper()
			var got map[string]any
			f.command("GET", "/api/bloem/v1/admin/platform/native-storage/capabilities", f.platformToken, "", nil, 200, &got)
			for _, key := range []string{"source_management", "approved_artifact_install", "configuration_replace_unbound", "disable", "uninstall", "binding_inspection", "binding_mutation"} {
				if got[key] != ready {
					t.Fatalf("capability %s=%v, want %v", key, got[key], ready)
				}
			}
			for _, key := range []string{"retained_namespace_reinstall", "enable", "backend_verified"} {
				if got[key] != false {
					t.Fatalf("unsupported capability %s promoted", key)
				}
			}
			ops, ok := got["supported_operations"].(map[string]any)
			if !ok {
				t.Fatal("missing supported operations")
			}
			for _, key := range []string{"initialize", "bind", "full_scan", "source_disable", "source_uninstall"} {
				if ops[key] != ready {
					t.Fatalf("operation %s did not follow composition", key)
				}
			}
			for _, key := range []string{"library_update", "scoped_scan", "repair", "delete", "unbind"} {
				if ops[key] != false {
					t.Fatalf("unsupported operation %s promoted", key)
				}
			}
			after, err := os.ReadFile(f.journal)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("capability inspection performed provider I/O")
			}
		}
		check(true)
		publisher.SetImageCacher(nil)
		check(false)
		var absent *imagecache.Cacher
		publisher.SetImageCacher(absent)
		check(false)
		publisher.SetImageCacher(cacher)
		check(true)
		// A separate guarded pool to the SAME private clone still violates the
		// required reader/database pool identity. No additional clone is created.
		foreign, err := pgxpool.NewWithConfig(t.Context(), f.pool.Config())
		if err != nil {
			t.Fatal("create guarded alternative reader pool")
		}
		defer foreign.Close()
		var database string
		if err := foreign.QueryRow(t.Context(), "SELECT current_database()").Scan(&database); err != nil || database != f.pool.Config().ConnConfig.Database {
			t.Fatal("alternative reader pool clone identity mismatch")
		}
		mismatch := d
		mismatch.FileRepo = scanner.NewFileRepository(foreign)
		f.support.rebuild(mismatch, nil)
		check(false)
		f.support.rebuild(d, nil)
		check(true)
		// The public request, not an injected readiness boolean, observes schema drift.
		if _, err = f.pool.Exec(t.Context(), "ALTER TABLE bloem_storage_sources RENAME COLUMN latest_installation_id TO capability_hidden_lineage"); err != nil {
			t.Fatal("apply owned clone schema drift")
		}
		restore := func() {
			t.Helper()
			restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer restoreCancel()
			if _, err := f.pool.Exec(restoreCtx, "ALTER TABLE bloem_storage_sources RENAME COLUMN capability_hidden_lineage TO latest_installation_id"); err != nil {
				t.Error("restore owned clone schema")
			}
		}
		func() { defer restore(); check(false) }()
		check(true)
		mismatch = d
		mismatch.LibraryScanQueue = f.support.queue
		f.support.rebuild(mismatch, nil)
		check(false)
		f.support.rebuild(d, nil)
		check(true)
		mismatch = d
		mismatch.LibraryIngester = libraryingest.NewExecutor(publisher, nil, catalog.NewFolderRepository(f.pool), nil, nil, nil)
		f.support.rebuild(mismatch, nil)
		check(false)
		f.support.rebuild(d, nil)
		check(true)
		queue.Stop()
		check(false)
	})
}
