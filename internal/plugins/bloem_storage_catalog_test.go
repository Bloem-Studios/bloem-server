package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: map[pinKey]bool{{pluginID: "bloem.storage.s3", checksum: checksum}: true}}
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

// catalogRow is one release in a multi-release test feed.
type catalogRow struct {
	spec     nativeCatalogSpec
	version  string
	checksum string
	url      string // overrides the well-formed binary URL when set
}

func catalogFixtureRows(t *testing.T, rows ...catalogRow) *http.Client {
	t.Helper()
	var plugins []any
	manifests := map[string][]byte{}
	for _, row := range rows {
		manifest := &publicv1.PluginManifest{PluginId: row.spec.PluginID, Version: row.version, SiloApiVersion: "v1", Checksum: row.checksum,
			SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
		raw, err := protojson.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		manifests["/Bloem-Studios/bloem-plugins/releases/download/"+row.spec.ReleaseTag+row.version+"/manifest.json"] = raw
		url := row.url
		if url == "" {
			url = "https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + row.spec.ReleaseTag + row.version + "/plugin-" + runtime.GOOS + "-" + runtime.GOARCH
		}
		plugins = append(plugins, map[string]any{"plugin_id": row.spec.PluginID, "source_version": row.version,
			"binaries": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]any{"url": url, "checksum": row.checksum}}})
	}
	inventory, _ := json.Marshal(map[string]any{"plugins": plugins})
	return &http.Client{Transport: storageCatalogTransport(func(r *http.Request) (*http.Response, error) {
		body := inventory
		if m, ok := manifests[r.URL.Path]; ok {
			body = m
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
}

func TestNativeCatalogPinIsPerPlugin(t *testing.T) {
	checksum := strings.Repeat("d", 64)
	// A statically approved Bookwarehouse artifact carries checksum X.
	pins := newNativeStoragePins(map[string]NativeStorageArtifact{"bw": {
		Manifest: &publicv1.PluginManifest{PluginId: "bloem.storage.bookwarehouse", Version: "0.2.2"}, Checksum: checksum}})
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: pins}
	registry.catalogClient = catalogFixture(t, s3Spec, "0.3.2", checksum)
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := registry.ApprovedArtifacts(); len(got) != 0 {
		t.Fatalf("another plugin's static approval admitted an S3 catalog release: %+v", got)
	}
}

func TestNativeCatalogMalformedUnpinnedS3RowDoesNotBlockBookwarehouse(t *testing.T) {
	good := strings.Repeat("e", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = catalogFixtureRows(t,
		catalogRow{spec: s3Spec, version: "0.3.2", checksum: "not-a-checksum", url: "https://attacker.example/plugin"},
		catalogRow{spec: bookwarehouseSpec, version: "0.2.2", checksum: good})
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatalf("a malformed unpinned S3 row broke the whole refresh: %v", err)
	}
	got := registry.ApprovedArtifacts()
	if len(got) != 1 || got[0].PluginID != "bloem.storage.bookwarehouse" {
		t.Fatalf("bookwarehouse not admitted: %+v", got)
	}
}

func TestNativeCatalogMalformedPinnedS3RowStillFailsClosed(t *testing.T) {
	checksum := strings.Repeat("f", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact),
		pinned: map[pinKey]bool{{pluginID: "bloem.storage.s3", checksum: checksum}: true}}
	registry.catalogClient = catalogFixtureRows(t, catalogRow{spec: s3Spec, version: "0.3.2", checksum: checksum, url: "https://attacker.example/plugin"})
	if err := registry.RefreshCatalog(context.Background()); err == nil {
		t.Fatal("malformed pinned S3 row was accepted")
	}
}

func TestNativeCatalogDoesNotRepersistCachedUnpinnedS3Records(t *testing.T) {
	root := t.TempDir()
	s3Checksum := strings.Repeat("1", 64)
	bwChecksum := strings.Repeat("2", 64)
	record := func(spec nativeCatalogSpec, version, checksum string) storageCatalogRecord {
		m := &publicv1.PluginManifest{PluginId: spec.PluginID, Version: version, SiloApiVersion: "v1", Checksum: checksum,
			SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
		raw, err := protojson.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return storageCatalogRecord{Manifest: raw, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH,
			URL: "https://github.com/Bloem-Studios/bloem-plugins/releases/download/" + spec.ReleaseTag + version + "/plugin-" + runtime.GOOS + "-" + runtime.GOARCH}
	}
	s3Key := catalogArtifactKey(s3Spec, "0.3.2", s3Checksum)
	bwKey := catalogArtifactKey(bookwarehouseSpec, "0.2.2", bwChecksum)
	cache, _ := json.Marshal(map[string]storageCatalogRecord{s3Key: record(s3Spec, "0.3.2", s3Checksum), bwKey: record(bookwarehouseSpec, "0.2.2", bwChecksum)})
	if err := os.WriteFile(filepath.Join(root, "native-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	if err := registry.loadCatalog(); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.catalogRecords[s3Key]; ok {
		t.Fatal("an unpinned S3 record was kept after load")
	}
	registry.catalogClient = catalogFixtureRows(t, catalogRow{spec: bookwarehouseSpec, version: "0.2.2", checksum: bwChecksum})
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	persisted, err := os.ReadFile(filepath.Join(root, "native-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), s3Key) || !strings.Contains(string(persisted), bwKey) {
		t.Fatalf("refresh re-persisted an unpinned S3 record or lost bookwarehouse: %s", persisted)
	}
}
