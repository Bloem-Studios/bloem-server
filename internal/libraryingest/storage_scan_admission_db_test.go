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
	"github.com/jackc/pgx/v5/pgxpool"
)

func scanAdmissionDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid disposable database configuration")
	}
	template := config.ConnConfig.Database
	if !strings.HasPrefix(template, "bloem_storage_test_") {
		t.Fatal("refusing non-owned database template")
	}
	adminConfig := config.Copy()
	adminConfig.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal("connect clone admin")
	}
	t.Cleanup(admin.Close)
	name := "bloem_storage_test_admission_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+pgx.Identifier{template}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.Database = name
	config.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal("connect owned clone")
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestStorageScanDescribeAdmissionBeforeDiscovery(t *testing.T) {
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binaryPath, "./testdata/nativeconsumer")
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
	for _, kind := range []string{"describe-source", "describe-root", "describe-duplicate", "describe-malformed", "false", "retained-root", "retained-source", "revision", "generation", "authorization", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			pool := scanAdmissionDatabase(t)
			cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
			if err != nil {
				t.Fatal(err)
			}
			registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
			if err != nil {
				t.Fatal(err)
			}
			var owner uuid.UUID
			if err := pool.QueryRow(t.Context(), "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			mode := kind
			gate := !strings.HasPrefix(kind, "describe-") && kind != "false"
			if gate {
				mode = "describe-gate"
			}
			notify := filepath.Join(t.TempDir(), "describe-started")
			installed, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary,
				Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true},
				Config: map[string]map[string]any{"source": {"mode": mode, "notify": notify}}})
			if err != nil {
				t.Fatal(err)
			}
			locations := nativestorage.NewLibraryLocations(pool)
			folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebooks", Name: "Describe admission",
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
			scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(pool), &scanner.Scanner{})
			if err != nil {
				t.Fatal(err)
			}
			request, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := scan.ScanStorageFolder(request, folder, storagesource.LocationSelection{Location: location})
				result <- err
			}()
			deadline := time.NewTimer(15 * time.Second)
			defer deadline.Stop()
			if gate {
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
						t.Fatalf("scan returned before Describe gate: %v", err)
					case <-deadline.C:
						t.Fatal("scan Describe did not reach gate")
					}
				}
				var query string
				var target any = installed.Source.Key
				switch kind {
				case "retained-root":
					query = "UPDATE bloem_storage_sources SET root_entry_id='changed-root' WHERE key=$1"
				case "retained-source":
					query = "UPDATE bloem_storage_sources SET provider_source_id='changed-source' WHERE key=$1"
				case "revision":
					query = "UPDATE bloem_storage_sources SET configuration_revision=configuration_revision+1 WHERE key=$1"
				case "generation":
					query, target = "UPDATE plugin_installations SET runtime_generation=runtime_generation+1 WHERE id=$1", installed.Installation.ID
				case "authorization":
					query, target = "UPDATE media_folders SET enabled=false WHERE id=$1", folder.ID
				case "cancel":
					cancel()
				}
				if query != "" {
					if _, err := pool.Exec(t.Context(), query, target); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(notify+".release", []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("unsafe scan admitted")
				}
				if kind == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("scan lost Describe cancellation", err)
				}
			case <-deadline.C:
				t.Fatal("scan did not finish")
			}
			if io, err := os.ReadFile(notify + ".io"); err == nil {
				t.Fatalf("scan reached provider content IO: %s", io)
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			var runs int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$1", installed.Source.Key).Scan(&runs); err != nil || runs != 0 {
				t.Fatalf("refused scan claimed discovery lease: runs=%d err=%v", runs, err)
			}
		})
	}
}
