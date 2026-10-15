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
	"google.golang.org/protobuf/proto"
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
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.2", checksum)}
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
	url      string                   // overrides the well-formed binary URL when set
	manifest *publicv1.PluginManifest // overrides the served manifest.json when set
}

func catalogFixtureRows(t *testing.T, rows ...catalogRow) *http.Client {
	t.Helper()
	var plugins []any
	manifests := map[string][]byte{}
	for _, row := range rows {
		manifest := row.manifest
		if manifest == nil {
			manifest = &publicv1.PluginManifest{PluginId: row.spec.PluginID, Version: row.version, SiloApiVersion: "v1", Checksum: row.checksum,
				SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
		}
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
		pinned: s3Pins(t, "0.3.2", checksum)}
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

// staticS3Manifest is the manifest a host's static approvals file carries for
// the S3 plugin; the catalog must reuse it rather than the served manifest.json.
func staticS3Manifest(version, checksum string) *publicv1.PluginManifest {
	return &publicv1.PluginManifest{PluginId: "bloem.storage.s3", Version: version, SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}},
		GlobalConfigSchema: []*publicv1.ConfigSchema{{Key: "connection", Title: "S3 storage connection", JsonSchema: `{"type":"object"}`}}}
}

// s3Pins pins one statically approved S3 artifact of the given version.
func s3Pins(t *testing.T, version, checksum string) map[pinKey]NativeStorageArtifact {
	t.Helper()
	return newNativeStoragePins(map[string]NativeStorageArtifact{"s3-static": {Manifest: staticS3Manifest(version, checksum), Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH}})
}

// servedS3Manifest differs from the static approval in schema and description,
// as a later or tampered manifest.json on the catalog repository would.
func servedS3Manifest(version, checksum string) *publicv1.PluginManifest {
	m := staticS3Manifest(version, checksum)
	m.GlobalConfigSchema = []*publicv1.ConfigSchema{
		{Key: "connection", Title: "S3 storage connection", Description: "served description", JsonSchema: `{"type":"object","properties":{"bucket":{"type":"string"}}}`},
		{Key: "extra", Title: "Extra", JsonSchema: `{"type":"object"}`},
	}
	m.Category = "served"
	return m
}

func catalogS3Artifact(t *testing.T, registry *NativeStorageRegistry) NativeStorageArtifact {
	t.Helper()
	var found []NativeStorageArtifact
	for key, a := range registry.artifactSnapshot() {
		if strings.HasPrefix(key, "s3-0.") {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one S3 catalog artifact, got %d", len(found))
	}
	return found[0]
}

func TestNativeCatalogPinnedS3UsesStaticApprovalManifest(t *testing.T) {
	checksum := strings.Repeat("3", 64)
	root := t.TempDir()
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.2", checksum)}
	registry.catalogClient = catalogFixtureRows(t, catalogRow{spec: s3Spec, version: "0.3.2", checksum: checksum, manifest: servedS3Manifest("0.3.2", checksum)})
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := staticS3Manifest("0.3.2", checksum)
	if got := catalogS3Artifact(t, registry); !proto.Equal(got.Manifest, want) {
		t.Fatalf("catalog S3 artifact took the served manifest:\n got %v\nwant %v", got.Manifest, want)
	}
	// The persisted record reloads to the static manifest too.
	reloaded := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.2", checksum)}
	if err := reloaded.loadCatalog(); err != nil {
		t.Fatal(err)
	}
	if got := catalogS3Artifact(t, reloaded); !proto.Equal(got.Manifest, want) {
		t.Fatalf("reloaded catalog S3 artifact differs from the static approval: %v", got.Manifest)
	}
}

func TestNativeCatalogLoadSubstitutesStaticManifestForCachedS3Record(t *testing.T) {
	checksum := strings.Repeat("4", 64)
	root := t.TempDir()
	raw, err := protojson.Marshal(servedS3Manifest("0.3.2", checksum))
	if err != nil {
		t.Fatal(err)
	}
	key := catalogArtifactKey(s3Spec, "0.3.2", checksum)
	cache, _ := json.Marshal(map[string]storageCatalogRecord{key: {Manifest: raw, Checksum: checksum, OS: runtime.GOOS, Arch: runtime.GOARCH,
		URL: "https://github.com/Bloem-Studios/bloem-plugins/releases/download/s3-v0.3.2/plugin-" + runtime.GOOS + "-" + runtime.GOARCH}})
	if err := os.WriteFile(filepath.Join(root, "native-catalog.json"), cache, 0600); err != nil {
		t.Fatal(err)
	}
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.2", checksum)}
	if err := registry.loadCatalog(); err != nil {
		t.Fatal(err)
	}
	if got := catalogS3Artifact(t, registry); !proto.Equal(got.Manifest, staticS3Manifest("0.3.2", checksum)) {
		t.Fatalf("cached S3 record kept its own manifest: %v", got.Manifest)
	}
}

func TestNativeCatalogSkipsS3RowWhenStaticApprovalVersionDiffers(t *testing.T) {
	checksum := strings.Repeat("5", 64)
	good := strings.Repeat("6", 64)
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.1", checksum)}
	registry.catalogClient = catalogFixtureRows(t,
		catalogRow{spec: s3Spec, version: "0.3.2", checksum: checksum},
		catalogRow{spec: bookwarehouseSpec, version: "0.2.2", checksum: good})
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := registry.ApprovedArtifacts()
	if len(got) != 1 || got[0].PluginID != "bloem.storage.bookwarehouse" {
		t.Fatalf("S3 row admitted under a static approval of another version: %+v", got)
	}
}

func TestNativeCatalogSkipsS3RowWhenServedManifestIdentityDiffers(t *testing.T) {
	checksum := strings.Repeat("7", 64)
	for name, served := range map[string]*publicv1.PluginManifest{
		"checksum": staticS3Manifest("0.3.2", strings.Repeat("8", 64)),
		"version":  staticS3Manifest("0.3.3", checksum),
	} {
		t.Run(name, func(t *testing.T) {
			registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact), pinned: s3Pins(t, "0.3.2", checksum)}
			registry.catalogClient = catalogFixtureRows(t, catalogRow{spec: s3Spec, version: "0.3.2", checksum: checksum, manifest: served})
			if err := registry.RefreshCatalog(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := registry.ApprovedArtifacts(); len(got) != 0 {
				t.Fatalf("S3 row with a mismatched served manifest was admitted: %+v", got)
			}
		})
	}
}
