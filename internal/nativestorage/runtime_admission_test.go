package nativestorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestManagedBorrowDescribeAdmission(t *testing.T) {
	ctx, stop := context.WithTimeout(t.Context(), 2*time.Minute)
	defer stop()
	binary := filepath.Join(t.TempDir(), "native-fixture")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "../storageplugin/testdata/nativefixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build admission fixture: %v\n%s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	checksum := hex.EncodeToString(sum[:])
	for _, mode := range []string{"change-source", "change-root", "unpinned", "request-cancel", "disable"} {
		t.Run(mode, func(t *testing.T) {
			m := storageplugin.NewManager(storageplugin.Config{RPCDeadline: 5 * time.Second, HealthInterval: time.Hour})
			t.Cleanup(func() {
				if err := m.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			started := filepath.Join(t.TempDir(), "describe-started")
			config, err := structpb.NewStruct(map[string]any{"token": "synthetic-source-secret", "describe_notify": started})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := storageplugin.Snapshot{InstallationID: 1, Generation: 1, BinaryPath: binary, ExpectedChecksum: checksum, Enabled: true, NativeOnly: true,
				Manifest: &publicv1.PluginManifest{PluginId: "bloem.runtime.fixture", Version: "0.1.0", Checksum: checksum, SiloApiVersion: "v1", SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}},
				Config:   []*publicv1.ConfigEntry{{Key: "source", Value: config}}}
			run := ManagedRuntime{Manager: m}
			first, err := run.Borrow(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := storageplugin.ValidateSourceDescription(first.Description, "fixture", "root"); err != nil {
				t.Fatal(err)
			}
			session, err := m.Ensure(ctx, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			entry := mode
			if mode == "request-cancel" || mode == "disable" {
				entry = "block-description"
			}
			if _, err := session.Provider().Stat(ctx, &storagev1.StatRequest{EntryId: entry}); err != nil {
				t.Fatal(err)
			}
			if mode != "request-cancel" && mode != "disable" {
				second, err := run.Borrow(ctx, snapshot)
				if err != nil {
					t.Fatal(err)
				}
				if err := storageplugin.ValidateSourceDescription(second.Description, "fixture", "root"); err == nil {
					t.Fatal("borrow reused the startup description")
				}
				if err := storageplugin.ValidateSourceDescription(first.Description, "fixture", "root"); err != nil {
					t.Fatal("later response changed retained lease ownership")
				}
				return
			}
			request, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				lease, err := run.Borrow(request, snapshot)
				if lease.Description != nil {
					err = errors.New("canceled borrow returned a description")
				}
				result <- err
			}()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(5 * time.Millisecond)
			defer poll.Stop()
		waiting:
			for {
				select {
				case <-poll.C:
					if _, err := os.Stat(started); err == nil {
						break waiting
					}
				case err := <-result:
					t.Fatalf("borrow returned before Describe blocked: %v", err)
				case <-deadline.C:
					t.Fatal("Describe was never entered")
				}
			}
			if mode == "disable" {
				m.Disable(snapshot.InstallationID)
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("canceled Describe borrow accepted")
				}
			case <-deadline.C:
				t.Fatal("Describe did not stop at cancellation")
			}
			if mode == "request-cancel" {
				if session.Context().Err() != nil {
					t.Fatal("borrower cancellation stopped resident")
				}
				if _, err := session.Provider().Stat(ctx, &storagev1.StatRequest{}); err != nil {
					t.Fatal("resident RPC unavailable after borrower cancellation", err)
				}
			} else {
				select {
				case <-session.Context().Done():
				case <-deadline.C:
					t.Fatal("disable did not fence generation")
				}
				if _, err := run.Borrow(ctx, snapshot); !errors.Is(err, storageplugin.ErrDisabled) {
					t.Fatalf("disabled generation started another Describe: %v", err)
				}
			}
		})
	}
}
