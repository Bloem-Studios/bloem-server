//go:build integration

package nativestorage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	"github.com/Silo-Server/silo-server/internal/storagesource"
	"github.com/google/uuid"
)

// A refused administrative action must not leave an attached library offline
// until restart. The subtests are the two live reproductions: "Connect" on a
// source with an attached library, and an upgrade with a rejected artifact.
// Only a committed generation bump keeps the previous generation fenced.
func TestFailedSourceMutationKeepsReadsAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	path := filepath.Join(t.TempDir(), "s3-fixture")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags=-s -w", "-o", path, "./testdata/s3installfixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build storage fixture: %v\n%s", err, out)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, code string
		mutate     func(*fencedSourceFixture) error
	}{
		{"refused connect", "configuration_namespace_unverified", func(f *fencedSourceFixture) error {
			_, err := f.management.ReplaceConfiguration(ctx, f.actor, f.installed.Source.Key, f.installed.Source.ConfigurationRevision, f.cmd.Config)
			return err
		}},
		{"rejected upgrade", "artifact_rejected", func(f *fencedSourceFixture) error {
			_, err := f.management.Upgrade(ctx, f.actor, f.installed.Installation.ID, UpgradeCommand{ArtifactKey: f.cmd.ArtifactKey, SourceKey: f.installed.Source.Key,
				ExpectedRevision: f.installed.Source.ConfigurationRevision, Binary: append(bytes.Clone(binary), 0)})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFencedSourceFixture(ctx, t, binary)
			before, err := f.read(ctx)
			if err != nil {
				t.Fatal("initial read", err)
			}
			err = tc.mutate(f)
			if refused, ok := errors.AsType[*catalog.NativeOnboardingError](err); !ok || refused.Code != tc.code {
				t.Fatalf("mutation = %v, want %s", err, tc.code)
			}
			after, err := f.read(ctx)
			if err != nil {
				t.Fatalf("read after %s: %v", tc.name, err)
			}
			if after.Generation != before.Generation || after.Source.ConfigurationRevision != before.Source.ConfigurationRevision {
				t.Fatalf("refused mutation committed: generation %d->%d revision %d->%d", before.Generation, after.Generation, before.Source.ConfigurationRevision, after.Source.ConfigurationRevision)
			}
			// A committed removal advances the generation. The previous
			// generation's view must stay fenced although it was readable.
			if err := f.management.Remove(ctx, f.actor, f.installed.Installation.ID, f.installed.Source.Key, after.Source.ConfigurationRevision, false); err != nil {
				t.Fatal("remove", err)
			}
			if err := borrowForRead(ctx, f.management.coordinator.Runtime, after); !errors.Is(err, storageplugin.ErrDisabled) {
				t.Fatalf("committed removal left the old generation borrowable: %v", err)
			}
		})
	}
}

type fencedSourceFixture struct {
	management *SourceManagement
	actor      auth.AdminContextClaims
	cmd        DiscoverInstallCommand
	installed  *plugins.NativeStorageSnapshot
}

// newFencedSourceFixture installs a runnable storage source with an attached
// ebook library and wires the production coordinator and runtime.
func newFencedSourceFixture(ctx context.Context, t *testing.T, binary []byte) *fencedSourceFixture {
	t.Helper()
	management, actor, cmd := discoveryFixture(t, binary, "", true)
	var owner uuid.UUID
	if err := management.pool.QueryRow(ctx, `SELECT bloem_platform_resource_owner_id()`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	installed, err := management.registry.Install(ctx, plugins.NativeStorageInstallRequest{ArtifactKey: cmd.ArtifactKey, Binary: binary, Config: cmd.Config,
		Source: storagesource.SourceConfig{OwnerID: owner, ProviderSourceID: cmd.ProviderSourceID, RootEntryID: installFixtureRoot, Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createStorageLibrary(t, management.pool, "ebooks", installed.Source.Key); err != nil {
		t.Fatal(err)
	}
	host := management.installDiscoveryHost
	management.SetCoordinator(&Coordinator{Registry: host.Registry, Runtime: ManagedRuntime{Manager: host.Manager}, AcquireOpen: host.AcquireOpen})
	return &fencedSourceFixture{management: management, actor: actor, cmd: cmd, installed: installed}
}

// read performs the runtime admission of an ebook or cover read: a fresh
// authorized snapshot, Borrow and the read Describe check.
func (f *fencedSourceFixture) read(ctx context.Context) (*plugins.NativeStorageSnapshot, error) {
	current, err := f.management.installDiscoveryHost.Registry.Snapshot(ctx, f.installed.Source.Key, f.installed.Source.OwnerID)
	if err != nil {
		return nil, err
	}
	return current, borrowForRead(ctx, f.management.coordinator.Runtime, current)
}

func borrowForRead(ctx context.Context, runtime Runtime, snapshot *plugins.NativeStorageSnapshot) error {
	lease, err := runtime.Borrow(ctx, storageplugin.Snapshot{InstallationID: snapshot.Installation.ID, Generation: snapshot.Generation, BinaryPath: snapshot.Installation.InstallPath, ExpectedChecksum: snapshot.ArtifactChecksum, Manifest: snapshot.Manifest, Config: snapshot.Config, Enabled: true, NativeOnly: true})
	if err != nil {
		return err
	}
	return ValidateReadDescription(lease.Description, snapshot.Source.ProviderSourceID, snapshot.Source.RootEntryID, snapshot)
}
