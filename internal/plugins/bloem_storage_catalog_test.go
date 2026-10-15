package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type storageCatalogTransport func(*http.Request) (*http.Response, error)

func (f storageCatalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var (
	bookwarehouseSpec = nativeCatalogSpec{PluginID: "bloem.storage.bookwarehouse", KeyPrefix: "bookwarehouse", ReleaseTag: "bookwarehouse-v"}
	s3Spec            = nativeCatalogSpec{PluginID: "bloem.storage.s3", KeyPrefix: "s3", ReleaseTag: "s3-v", RequirePinned: true}
)

// catalogFixture serves a one-release inventory and manifest for spec/version.
func catalogFixture(t *testing.T, spec nativeCatalogSpec, version, checksum string) *http.Client {
	t.Helper()
	manifest := &publicv1.PluginManifest{PluginId: spec.PluginID, Version: version, SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	raw, err := protojson.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + spec.ReleaseTag + version + "/plugin-" + runtime.GOOS + "-" + runtime.GOARCH
	inventory, _ := json.Marshal(map[string]any{"plugins": []any{map[string]any{"plugin_id": spec.PluginID, "source_version": version,
		"binaries": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]any{"url": url, "checksum": checksum}}}}})
	return &http.Client{Transport: storageCatalogTransport(func(r *http.Request) (*http.Response, error) {
		body := inventory
		if strings.HasSuffix(r.URL.Path, "manifest.json") {
			body = raw
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
}

func TestNativeCatalogAdmitsWithoutManualApprovalAndPersists(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	root := t.TempDir()
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = catalogFixture(t, bookwarehouseSpec, "0.2.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CatalogBinary(context.Background(), catalogArtifactKey(bookwarehouseSpec, "0.2.2", checksum)); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	artifacts := registry.ApprovedArtifacts()
	if len(artifacts) != 1 || artifacts[0].PluginID != "bloem.storage.bookwarehouse" || !artifacts[0].Catalog {
		t.Fatalf("catalog missing: %+v", artifacts)
	}
	reloaded := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	if err := reloaded.loadCatalog(); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ApprovedArtifacts()) != 1 {
		t.Fatal("installed catalog trust requires manual approval or refresh after restart")
	}
}

func TestNativeCatalogS3RequiresPinnedDigest(t *testing.T) {
	checksum := strings.Repeat("b", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = catalogFixture(t, s3Spec, "0.3.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := registry.ApprovedArtifacts(); len(got) != 0 {
		t.Fatalf("unpinned S3 release was admitted: %+v", got)
	}
}

func TestNativeCatalogAdmitsPinnedS3AndFlagsCatalog(t *testing.T) {
	checksum := strings.Repeat("c", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: map[string]bool{checksum: true}}
	registry.catalogClient = catalogFixture(t, s3Spec, "0.3.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := registry.ApprovedArtifacts()
	if len(got) != 1 || got[0].PluginID != "bloem.storage.s3" || !got[0].Catalog || !strings.HasPrefix(got[0].ArtifactKey, "s3-0.3.2-") {
		t.Fatalf("pinned S3 release not admitted as catalog artifact: %+v", got)
	}
}

func TestNativeCatalogRejectsExternalBinaryURLs(t *testing.T) {
	if storageCatalogURL(bookwarehouseSpec, "https://attacker.example/plugin", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("external URL accepted")
	}
	if storageCatalogURL(bookwarehouseSpec, "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.2.2/plugin-linux-amd64?token=secret", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("query accepted")
	}
	if storageCatalogURL(s3Spec, "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.3.2/plugin-linux-amd64", "0.3.2", "plugin-linux-amd64") {
		t.Fatal("another plugin's release tag accepted for S3")
	}
}
