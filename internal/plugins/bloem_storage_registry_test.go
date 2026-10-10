package plugins

import (
	"os"
	"runtime"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func nativeS3PrivateRoot(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// /tmp is intentionally inadmissible: its writable ancestor would permit
	// namespace replacement. Keep these fixtures in a host-owned private tree.
	root, err := os.MkdirTemp(home, ".bloem-s3-state-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func approvedNativeFixture() NativeStorageArtifact {
	checksum := strings.Repeat("a", 64)
	return NativeStorageArtifact{Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH, Manifest: &publicv1.PluginManifest{PluginId: "bloem.storage.fixture", Version: "1.0.0", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}}
}
func TestNativeStorageApprovalIsImmutable(t *testing.T) {
	artifact := approvedNativeFixture()
	registry, err := newNativeStorageApprovals(map[string]NativeStorageArtifact{"fixture": artifact})
	if err != nil {
		t.Fatal(err)
	}
	artifact.Manifest.PluginId = "silo.builtin"
	if registry["fixture"].Manifest.GetPluginId() != "bloem.storage.fixture" {
		t.Fatal("approval aliases caller manifest")
	}
}
func TestNativeStoragePrivateValidation(t *testing.T) {
	for _, kind := range []string{"reserved", "checksum", "platform", "path", "capability", "route"} {
		t.Run(kind, func(t *testing.T) {
			a := approvedNativeFixture()
			switch kind {
			case "reserved":
				a.Manifest.PluginId = "silo.builtin"
			case "checksum":
				a.Checksum = "bad"
			case "platform":
				a.OS = "other"
			case "path":
				a.Manifest.Assets = []*publicv1.PackagedAsset{{Path: "../escape"}}
			case "capability":
				a.Manifest.Capabilities = []*publicv1.CapabilityDescriptor{{Type: "storage", Id: "private"}}
			case "route":
				a.Manifest.HttpRoutes = []*publicv1.HttpRouteDescriptor{{Path: "/unsafe"}}
			}
			if _, err := newNativeStorageApprovals(map[string]NativeStorageArtifact{"fixture": a}); err == nil {
				t.Fatal("invalid native approval accepted")
			}
		})
	}
}
