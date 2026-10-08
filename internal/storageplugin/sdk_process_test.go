package storageplugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bloemtestsdk"
	"github.com/Silo-Server/silo-server/internal/mediasource"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Builds the SDK's hello-storage provider from the pinned published SDK release
// (or BLOEM_STORAGE_SDK_WORKTREE) and drives it through the manager.
func TestPrivateSDKExecutableThroughManager(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sdk := bloemtestsdk.StorageDir(t, ctx)
	binary := filepath.Join(t.TempDir(), "hello-storage")
	bloemtestsdk.BuildStorageFixture(t, ctx, sdk, binary, "-race")
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
