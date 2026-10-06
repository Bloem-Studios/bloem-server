package storageplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// This cross-repository check is opt-in: the standalone host has no dependency
// on an unpublished SDK checkout. Local executable lifecycle tests always run.
func TestPrivateSDKExecutableThroughManager(t *testing.T) {
	sdk := os.Getenv("BLOEM_STORAGE_SDK_WORKTREE")
	if sdk == "" {
		t.Skip("set BLOEM_STORAGE_SDK_WORKTREE to run private SDK executable conformance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "hello-storage")
	build := exec.CommandContext(ctx, "go", "build", "-race", "-o", binary, "./examples/hello-storage")
	build.Dir = sdk
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("SDK fixture build: %v\n%s", err, out)
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	digest := hex.EncodeToString(sum[:])
	embedded, err := os.ReadFile(filepath.Join(sdk, "examples", "hello-storage", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := &publicv1.PluginManifest{}
	if err := protojson.Unmarshal(embedded, manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Checksum = digest
	t.Setenv("BLOEM_STORAGE_TEST_SENTINEL", "synthetic-parent-secret")
	m := manager(t)
	s, err := m.Ensure(ctx, Snapshot{InstallationID: 2, Generation: 1, BinaryPath: binary, ExpectedChecksum: digest, Manifest: manifest, Enabled: true, NativeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	description, err := s.Provider().Describe(ctx, &storagev1.DescribeRequest{})
	if err != nil || description.GetRevision() != 1 || len(description.GetSources()) != 2 {
		t.Fatalf("private descriptor: %v %v", description, err)
	}
	f, err := mediasource.Open(ctx, mediasource.NewPluginSource(s.Provider()), mediasource.Ref{SourceID: "fixture", EntryID: "book", Revision: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header := make([]byte, 4)
	if _, err := f.ReadAt(header, 0); err != nil || string(header) != "PK\x03\x04" {
		t.Fatalf("SDK pinned ebook bytes: %q %v", header, err)
	}
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s)
	t.Logf("private SDK executable sha256=%s", digest)
}
