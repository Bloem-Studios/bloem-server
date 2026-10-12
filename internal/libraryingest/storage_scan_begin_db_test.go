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
	"sync/atomic"
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

type scanBeginGateKey struct{}

// The scanner's second registry snapshot starts after its final standalone
// authority transaction commits. Pause here without a production test hook.
type scanBeginGate struct {
	snapshots atomic.Int32
	entered   chan struct{}
	release   chan struct{}
}

func (g *scanBeginGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if ctx.Value(scanBeginGateKey{}) != g || !strings.Contains(data.SQL, "begin isolation level repeatable read") || g.snapshots.Add(1) != 2 {
		return ctx
	}
	close(g.entered)
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return ctx
}

func (*scanBeginGate) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// A preflight check cannot grant later journal writes. Removing the retained
// Begin authority fence creates a run or takes over an expired run after the
// production library update has revoked scan authority.
func TestStorageScanRevocationBeforeBeginPreservesJournal(t *testing.T) {
	if os.Getenv("SILO_TEST_DATABASE_URL") == "" {
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	binaryPath := filepath.Join(t.TempDir(), "native-consumer")
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags=-s -w", "-o", binaryPath, "./testdata/nativeconsumer")
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
	for _, mode := range []string{"new", "resume"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			pool := scanAdmissionDatabase(t)
			gate := &scanBeginGate{entered: make(chan struct{}), release: make(chan struct{})}
			cfg := pool.Config()
			cfg.ConnConfig.Tracer = gate
			traced, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(traced.Close)
			cipher, err := secret.New([]byte(strings.Repeat("synthetic-test-key", 3)))
			if err != nil {
				t.Fatal(err)
			}
			registry, err := plugins.NewNativeStorageRegistry(traced, cipher, t.TempDir(), map[string]plugins.NativeStorageArtifact{
				"fixture": {Manifest: manifest, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH},
			})
			if err != nil {
				t.Fatal(err)
			}
			var owner uuid.UUID
			if err := pool.QueryRow(ctx, "SELECT bloem_platform_resource_owner_id()").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			notify := filepath.Join(t.TempDir(), "provider-io")
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
			folder, err := folders.Create(ctx, catalog.CreateFolderInput{Type: "ebooks", Name: "Begin authority",
				AttachStorage: func(ctx context.Context, tx pgx.Tx, id int) error {
					return locations.AttachTx(ctx, tx, installed.Source.Key, id)
				}})
			if err != nil {
				t.Fatal(err)
			}
			sources := storagesource.NewRepository(traced)
			location, ok, err := sources.FolderLocation(ctx, folder.ID)
			if err != nil || !ok {
				t.Fatal("storage location missing", err)
			}
			var prior storagesource.Lease
			if mode == "resume" {
				prior, err = sources.Begin(ctx, installed.Source.Key, "previous-worker", time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, "UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", prior.RunID); err != nil {
					t.Fatal(err)
				}
			}
			host := &nativestorage.Host{Registry: registry, Manager: storageplugin.NewManager(storageplugin.Config{HealthInterval: time.Hour})}
			t.Cleanup(func() {
				shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := host.Shutdown(shutdown); err != nil {
					t.Error(err)
				}
			})
			publisher := scanner.NewScanner(scanner.NewFileRepository(pool), "", nil, 1, false, 0)
			scan, err := NewStorageScanner(host, sources, resourcetenancy.NewStore(traced), publisher)
			if err != nil {
				t.Fatal(err)
			}
			scan.leaseTTL = time.Hour
			request := context.WithValue(ctx, scanBeginGateKey{}, gate)
			result := make(chan error, 1)
			go func() {
				_, err := scan.ScanStorageFolder(request, folder, storagesource.LocationSelection{Location: location})
				result <- err
			}()
			select {
			case <-gate.entered:
			case err := <-result:
				t.Fatalf("scan returned before post-preflight gate: %v", err)
			case <-ctx.Done():
				t.Fatal("scan did not reach post-preflight gate")
			}
			enabled := false
			if err := folders.Update(ctx, folder.ID, catalog.UpdateFolderInput{Enabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			close(gate.release)
			select {
			case err := <-result:
				if !errors.Is(err, resourcetenancy.ErrResourceHidden) {
					t.Errorf("revoked scan must fail authority: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("scan did not finish")
			}
			var runs, directories int
			var discovery *uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM bloem_storage_scan_runs WHERE source_key=$1),
				(SELECT count(*) FROM bloem_storage_scan_directories d JOIN bloem_storage_scan_runs r ON r.id=d.run_id WHERE r.source_key=$1),
				discovery_run_id FROM bloem_storage_sources WHERE key=$1`, installed.Source.Key).Scan(&runs, &directories, &discovery); err != nil {
				t.Fatal(err)
			}
			t.Logf("revoked begin=%s runs=%d directories=%d discovery=%v", mode, runs, directories, discovery)
			if mode == "new" {
				if runs != 0 || directories != 0 || discovery != nil {
					t.Errorf("revoked scan created journal: runs=%d directories=%d discovery=%v", runs, directories, discovery)
				}
			} else {
				var epoch int64
				var actualOwner string
				var expired bool
				if err := pool.QueryRow(ctx, "SELECT lease_epoch,owner,lease_until<clock_timestamp() FROM bloem_storage_scan_runs WHERE id=$1", prior.RunID).Scan(&epoch, &actualOwner, &expired); err != nil {
					t.Fatal(err)
				}
				t.Logf("prior run epoch=%d owner_preserved=%v expired=%v", epoch, actualOwner == "previous-worker", expired)
				if runs != 1 || directories != 1 || discovery == nil || *discovery != prior.RunID || epoch != 1 || actualOwner != "previous-worker" || !expired {
					t.Error("revoked scan mutated prior journal or took over its lease")
				}
			}
			if io, err := os.ReadFile(notify + ".io"); err == nil {
				t.Errorf("revoked scan reached provider content I/O: %s", io)
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		})
	}
}
