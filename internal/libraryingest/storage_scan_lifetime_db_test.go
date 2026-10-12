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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/resourcetenancy"
	"github.com/Silo-Server/silo-server/internal/scanner"
	"github.com/Silo-Server/silo-server/internal/secret"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exported only in the test build to exercise the real queue without creating
// a libraryingest -> scanqueue import cycle or a production fixture hook.
type Task4NativeFixture struct {
	Pool        *pgxpool.Pool
	Scanner     *StorageScanner
	Folder      *models.MediaFolder
	Location    storagesource.Location
	Source      storagesource.SourceConfig
	Notify      string
	ProcessPath string
	JobContexts chan context.Context
}

func Task4NativeArtifact(t *testing.T) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "nativeconsumer")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", p, "./testdata/nativeconsumer")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nativeconsumer build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func Task4NewNativeFixture(t *testing.T, binary []byte, mode string) *Task4NativeFixture {
	t.Helper()
	pool := scanAdmissionDatabase(t)
	sum := sha256.Sum256(binary)
	manifest := &publicv1.PluginManifest{PluginId: "bloem.consumer.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: hex.EncodeToString(sum[:]), SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}, GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "source", JsonSchema: `{"type":"object","properties":{"mode":{"type":"string"},"notify":{"type":"string"},"revision":{"type":"string"}},"additionalProperties":false}`}}}
	cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := plugins.NewNativeStorageRegistry(pool, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{"fixture": {Manifest: manifest, Checksum: manifest.Checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
	if err != nil {
		t.Fatal(err)
	}
	var owner uuid.UUID
	if err = pool.QueryRow(t.Context(), `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	notify := filepath.Join(t.TempDir(), "provider-gate")
	installed, err := registry.Install(t.Context(), plugins.NativeStorageInstallRequest{ArtifactKey: "fixture", Binary: binary, Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: "books", RootEntryID: "root", Enabled: true}, Config: map[string]map[string]any{"source": {"mode": mode, "notify": notify}}})
	if err != nil {
		t.Fatal(err)
	}
	locations := nativestorage.NewLibraryLocations(pool)
	folder, err := catalog.NewFolderRepository(pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebooks", Name: "Task4 native lifetime", AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
		return locations.AttachTx(ctx, tx, installed.Source.Key, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	sources := storagesource.NewRepository(pool)
	location, ok, err := sources.FolderLocation(t.Context(), folder.ID)
	if err != nil || !ok {
		t.Fatal("location", err)
	}
	host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: 100 * time.Millisecond, HealthFailureLimit: 1})}
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
	scan.leaseTTL = time.Hour
	jobs := make(chan context.Context, 1)
	tracedCfg := pool.Config()
	tracedCfg.ConnConfig.Tracer = &task4QueryTrace{jobs: jobs}
	traced, err := pgxpool.NewWithConfig(t.Context(), tracedCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(traced.Close)
	scan.sources = storagesource.NewRepository(traced)
	scan.resources = resourcetenancy.NewStore(traced)
	if mode == "list-metadata-gate-renewal" {
		t.Fatal("use finite existing metadata fixture")
	}
	return &Task4NativeFixture{Pool: pool, Scanner: scan, Folder: folder, Location: location, Source: installed.Source, Notify: notify, ProcessPath: installed.Installation.InstallPath, JobContexts: jobs}
}
func Task4FastRenewalForTest(f *Task4NativeFixture) { f.Scanner.leaseTTL = 300 * time.Millisecond }
func Task4WaitProvider(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("provider signal missing")
		case <-ticker.C:
		}
	}
}

func TestStorageSourceContentionWaitRechecksAuthorityDB(t *testing.T) {
	binary := Task4NativeArtifact(t)
	for _, mode := range []string{"release", "revocation", "parent-cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := Task4NewNativeFixture(t, binary, "list-metadata-gate")
			sources := storagesource.NewRepository(f.Pool)
			locations := nativestorage.NewLibraryLocations(f.Pool)
			other, err := catalog.NewFolderRepository(f.Pool).Create(t.Context(), catalog.CreateFolderInput{Type: "ebooks", Name: "Contended location", AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
				return locations.AttachTx(ctx, tx, f.Source.Key, id)
			}})
			if err != nil {
				t.Fatal(err)
			}
			b, ok, err := sources.FolderLocation(t.Context(), other.ID)
			if err != nil || !ok {
				t.Fatal(err)
			}
			authorize := func(ctx context.Context, tx pgx.Tx) error {
				return f.Scanner.resources.RequireStorageScanTx(ctx, tx, f.Location, f.Source)
			}
			held, err := sources.BeginLocationAuthorized(t.Context(), f.Location, "held", time.Minute, authorize)
			if err != nil {
				t.Fatal(err)
			}
			request, cancel := context.WithCancel(t.Context())
			attempts := make(chan struct{})
			request = context.WithValue(request, task4AttemptKey{}, attempts)
			defer cancel()
			returned := make(chan error, 1)
			go func() {
				_, err := f.Scanner.ScanStorageFolder(request, other, storagesource.LocationSelection{Location: b})
				returned <- err
			}()
			select {
			case <-attempts:
			case err := <-returned:
				t.Fatalf("contention returned early: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("no contention attempt")
			}

			switch mode {
			case "release":
				cp, _, err := sources.NextDirectory(t.Context(), held)
				if err != nil {
					t.Fatal(err)
				}
				if err = sources.ApplyPage(t.Context(), held, cp, &storagev1.ListResponse{Complete: true}, nil); err != nil {
					t.Fatal(err)
				}
				if err = sources.Complete(t.Context(), held); err != nil {
					t.Fatal(err)
				}
				Task4WaitProvider(t, f.Notify)
				if err = os.WriteFile(f.Notify+".release", []byte("release"), 0600); err != nil {
					t.Fatal(err)
				}
			case "revocation":
				if _, err = f.Pool.Exec(t.Context(), `UPDATE bloem_storage_selected_roots SET enabled=false WHERE location_id=$1`, b.ID); err != nil {
					t.Fatal(err)
				}
			case "parent-cancel":
				cancel()
			}
			select {
			case err := <-returned:
				if mode == "release" && err != nil {
					t.Fatal(err)
				}
				if mode == "revocation" && !errors.Is(err, resourcetenancy.ErrResourceHidden) {
					t.Fatalf("wait did not recheck authority: %v", err)
				}
				if mode == "parent-cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("wait lost parent cancellation: %v", err)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("contention did not stop")
			}
			if mode != "release" {
				if _, err = os.Stat(f.Notify); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("contended source reached provider: %v", err)
				}
			}
		})
	}
}

type task4AttemptKey struct{}
type task4QueryTrace struct {
	jobs                 chan context.Context
	jobOnce, attemptOnce sync.Once
}

func (g *task4QueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT provider_source_id FROM bloem_storage_sources") {
		g.jobOnce.Do(func() { g.jobs <- ctx })
	}
	if strings.Contains(data.SQL, "SELECT lease_until>clock_timestamp(),EXISTS") {
		if entered, ok := ctx.Value(task4AttemptKey{}).(chan struct{}); ok {
			g.attemptOnce.Do(func() { close(entered) })
		}
	}
	return ctx
}
func (*task4QueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func Task4KillProcessForTest(t *testing.T, f *Task4NativeFixture) {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink("/proc/" + entry.Name() + "/exe")
		if err != nil || target != f.ProcessPath {
			continue
		}
		process, err := os.FindProcess(pid)
		if err != nil {
			t.Fatal(err)
		}
		if err = process.Kill(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("owned nativeconsumer process not found")
}
