//go:build integration

package libraryingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
)

// A committed last page does not authorize later completion or catalog cleanup:
// disabling the library before those writes must preserve its existing catalog.
func TestStorageScanRevocationAfterFinalPagePreventsCompletionAndSweep(t *testing.T) {
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags=-s -w", "-o", binaryPath, "./testdata/nativeconsumer")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native consumer: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`}}}
	for _, mode := range []string{"library", "installation", "authorized", "trash-disabled", "grace", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) { testStorageScanCompletionAuthority(t, binary, manifest, mode) })
	}
}

func testStorageScanCompletionAuthority(t *testing.T, binary []byte, manifest *publicv1.PluginManifest, mode string) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	pool := scanAdmissionDatabase(t)
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{
		"fixture": {Manifest: manifest, Checksum: manifest.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH},
	})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	notify := filepath.Join(t.TempDir(), "list-started")
	// List remains finite and metadata-only. The existing progress reporter is
	// the deterministic gate after the final page has committed.
	if err := os.WriteFile(notify+".release", []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	installed, err := registry.Install(ctx, plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
		Config: map[string]map[string]any{"source": {"mode": "list-metadata-gate", "notify": notify}}})
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	folders := catalog.NewFolderRepository(pool)
	folder, err := folders.Create(ctx, catalog.CreateFolderInput{Type: "ebooks", Name: "Completion authority",
		AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
			return locations.AttachTx(ctx, tx, installed.Source.Key, id)
		}})
	if err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	location, ok, err := sources.FolderLocation(ctx, folder.ID)
	if err != nil || !ok {
		t.Fatal("storage location missing", err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour})}
	t.Cleanup(func() {
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := host.Shutdown(shutdown); err != nil {
			t.Error(err)
		}
	})
	grace := time.Duration(0)
	if mode == "grace" {
		grace = 48 * time.Hour
	}
	publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, mode != "trash-disabled", grace)
	// Seed a previously removed native ebook through the real publication path,
	// with a separate identity so the new provider page cannot revive it.
	entry := &storagev1.Entry{Id: "previously-removed", Name: "removed.epub", LogicalPath: "Books/removed.epub", Revision: "v0", Size: 10,
		Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Ebook: &storagev1.EbookMetadata{Title: "Previously removed book", Authors: []string{"Earlier Writer"}}}
	book, ok := scanner.StorageEbookFromEntry(entry)
	if !ok {
		t.Fatal("missing seed metadata")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := publisher.PublishStorageEbooksTx(ctx, tx, folder, location, installed.Source.ConfigurationRevision, []scanner.StorageEbook{book}); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.MarkStorageEbooksRemovedTx(ctx, tx, location, []string{entry.Id}); err != nil {
		t.Fatal(err)
	}
	var oldFile int
	var oldItem string
	if err := tx.QueryRow(ctx, `UPDATE media_files f SET missing_since=now()-interval '1 day'
		FROM bloem_storage_file_refs r WHERE r.media_file_id=f.id AND r.location_id=$1 AND r.entry_id=$2
		RETURNING f.id,f.content_id`, location.ID, entry.Id).Scan(&oldFile, &oldItem); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(pool), publisher)
	if err != nil {
		t.Fatal(err)
	}
	// The long lease prevents periodic renewal from masking a missing final fence.
	scan.leaseTTL = time.Hour
	finalPage := false
	ctx = WithProgressReporter(ctx, func(update ProgressUpdate) {
		if update.Phase != "processing" || update.FilesProcessed != 5 || finalPage {
			return
		}
		var pending bool
		var state string
		if err := pool.QueryRow(ctx, `SELECT r.state,EXISTS(SELECT 1 FROM bloem_storage_scan_directories d WHERE d.run_id=r.id AND NOT d.complete)
			FROM bloem_storage_scan_runs r WHERE r.source_key=$1 AND r.state='running'`, installed.Source.Key).Scan(&state, &pending); err != nil {
			t.Fatal(err)
		}
		if state != "running" || pending {
			t.Fatalf("final page gate: state=%s pending=%v", state, pending)
		}
		switch mode {
		case "library":
			enabled := false
			if err := folders.Update(ctx, folder.ID, catalog.UpdateFolderInput{Enabled: &enabled}); err != nil {
				t.Fatal(err)
			}
		case "installation":
			if _, err := pool.Exec(ctx, "UPDATE plugin_installations SET enabled=false WHERE id=$1", installed.Installation.ID); err != nil {
				t.Fatal(err)
			}
		case "cleanup-failure":
			// Fail after the finalizer has changed catalog rows: none of its
			// changes may survive the rejected journal completion.
			if _, err := pool.Exec(ctx, fmt.Sprintf("ALTER TABLE bloem_storage_scan_runs ADD CONSTRAINT test_owned_completion_failure CHECK(source_key <> '%s'::uuid OR state <> 'complete')", installed.Source.Key)); err != nil {
				t.Fatal(err)
			}
		}
		finalPage = true
	})
	result, scanErr := scan.ScanStorageFolder(ctx, folder, storagesource.LocationSelection{Location: location})
	if !finalPage {
		t.Fatal("scan did not reach final committed page")
	}
	var oldFiles, oldMemberships, oldItems int
	var runState string
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM media_files WHERE id=$1),
		(SELECT count(*) FROM media_item_libraries WHERE content_id=$2 AND media_folder_id=$3),
		(SELECT count(*) FROM media_items WHERE content_id=$2),
		(SELECT state FROM bloem_storage_scan_runs WHERE source_key=$4 ORDER BY created_at DESC LIMIT 1)`,
		oldFile, oldItem, folder.ID, installed.Source.Key).Scan(&oldFiles, &oldMemberships, &oldItems, &runState); err != nil {
		t.Fatal(err)
	}
	t.Logf("completion authority=%s: err=%v run=%s old_files=%d old_memberships=%d old_items=%d files_deleted=%d memberships_removed=%d items_deleted=%d",
		mode, scanErr, runState, oldFiles, oldMemberships, oldItems, result.ScanResult.FilesDeleted, result.ScanResult.MembershipsRemoved, result.ScanResult.ItemsDeleted)
	if mode == "authorized" || mode == "trash-disabled" || mode == "grace" {
		wantFiles, wantDeleted := 0, 1
		if mode != "authorized" {
			wantFiles, wantDeleted = 1, 0
		}
		if scanErr != nil || runState != "complete" || oldFiles != wantFiles || oldMemberships != 0 || oldItems != 0 || result.ScanResult.FilesDeleted != wantDeleted || result.ScanResult.MembershipsRemoved != 1 || result.ScanResult.ItemsDeleted != 1 {
			t.Errorf("authorized completion did not sweep expired native ebook: err=%v run=%s files=%d memberships=%d items=%d deleted=%d",
				scanErr, runState, oldFiles, oldMemberships, oldItems, result.ScanResult.FilesDeleted)
		}
		return
	}
	if mode == "cleanup-failure" {
		if scanErr == nil {
			t.Error("completion failure was not reported")
		}
	} else if !errors.Is(scanErr, resourcetenancy.ErrResourceHidden) {
		t.Errorf("revoked finalization must fail resource authority: %v", scanErr)
	}
	if result.ScanResult.MembershipsRemoved != 0 || result.ScanResult.ItemsDeleted != 0 {
		t.Error("uncommitted sweep counters escaped failed completion")
	}
	if runState == "complete" {
		t.Error("revoked scan committed successful completion")
	}
	if oldFiles != 1 || oldMemberships != 1 || oldItems != 1 {
		t.Errorf("revoked sweep changed preserved catalog: files=%d memberships=%d items=%d", oldFiles, oldMemberships, oldItems)
	}
}
