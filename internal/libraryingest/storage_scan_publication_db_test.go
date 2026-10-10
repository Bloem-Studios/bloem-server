//go:build integration

package libraryingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Removing the transaction authority fence lets metadata-only pages publish
// after an installation or library is disabled while List is in flight.
func TestStorageScanRevocationDuringListPreventsPublication(t *testing.T) {
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binaryPath, "./testdata/nativeconsumer")
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
	for _, revoked := range []string{"library", "installation"} {
		t.Run(revoked, func(t *testing.T) {
			pool := scanAdmissionDatabase(t)
			cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
			if err != nil {
				t.Fatal(err)
			}
			registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{
				"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH},
			})
			if err != nil {
				t.Fatal(err)
			}
			var owner uuid.UUID
			if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			notify := filepath.Join(t.TempDir(), "list-started")
			installed, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
				Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
				Config: map[string]map[string]any{"source": {"mode": "list-metadata-gate", "notify": notify}}})
			if err != nil {
				t.Fatal(err)
			}
			locations := nativestorage.NewLibraryLocations(pool)
			folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebooks", Name: "Publication admission",
				AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
					return locations.AttachTx(ctx, tx, installed.Source.Key, id)
				}})
			if err != nil {
				t.Fatal(err)
			}
			sources := storagesource.NewRepository(pool)
			location, ok, err := sources.FolderLocation(t.Context(), folder.ID)
			if err != nil || !ok {
				t.Fatal("storage location missing", err)
			}
			host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour})}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := host.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
			scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(pool), publisher)
			if err != nil {
				t.Fatal(err)
			}
			// No renewal can conceal the missing publication fence in this
			// bounded test. The scan lease remains valid.
			scan.leaseTTL = time.Hour
			request, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := scan.ScanStorageFolder(request, folder, location); result <- err }()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waiting:
			for {
				select {
				case <-poll.C:
					if _, err := os.Stat(notify); err == nil {
						break waiting
					}
				case err := <-result:
					t.Fatalf("scan returned before List gate: %v", err)
				case <-request.Done():
					t.Fatal("List did not reach gate")
				}
			}
			query, target := "UPDATE media_folders SET enabled=false WHERE id=$1", folder.ID
			if revoked == "installation" {
				query, target = "UPDATE plugin_installations SET enabled=false WHERE id=$1", installed.Installation.ID
			}
			if _, err := pool.Exec(t.Context(), query, target); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(notify+".release", []byte("release"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, resourcetenancy.ErrResourceHidden) {
					t.Errorf("scan must refuse revoked publication: %v", err)
				}
			case <-request.Done():
				t.Fatal("scan did not finish")
			}
			var files, memberships, entries, cursors int
			if err := pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM media_files WHERE media_folder_id=$1),
				(SELECT count(*) FROM media_item_libraries WHERE media_folder_id=$1),
				(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$2),
				(SELECT count(*) FROM bloem_storage_scan_cursors c JOIN bloem_storage_scan_runs r ON r.id=c.run_id WHERE r.source_key=$2)`,
				folder.ID, installed.Source.Key).Scan(&files, &memberships, &entries, &cursors); err != nil {
				t.Fatal(err)
			}
			if files != 0 || memberships != 0 || entries != 0 || cursors != 0 {
				t.Errorf("revoked scan committed files=%d memberships=%d entries=%d cursors=%d", files, memberships, entries, cursors)
			}
		})
	}
}
