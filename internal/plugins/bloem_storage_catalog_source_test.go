package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"testing"

	publicv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

var ownHostSource = NativeCatalogSource{
	CatalogURL: "https://plugins.example.org/plugins.json",
	ReleaseURL: "https://plugins.example.org/releases/",
	Hosts:      []string{"plugins.example.org"},
}

func ownHostRegistry(t *testing.T, root string) *NativeStorageRegistry {
	t.Helper()
	c, err := ownHostSource.parse()
	if err != nil {
		t.Fatal(err)
	}
	return &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact), source: c}
}

func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// Without settings the server reads exactly what it read before the source
// became configurable.
func TestNativeCatalogSourceDefaultsToGitHub(t *testing.T) {
	source, err := NativeCatalogSourceFromEnv(envOf(nil))
	if err != nil {
		t.Fatal(err)
	}
	c, err := source.parse()
	if err != nil {
		t.Fatal(err)
	}
	if c.catalogURL != "https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/plugins.json" {
		t.Fatalf("catalog URL = %q", c.catalogURL)
	}
	want := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.3.12/plugin-linux-amd64"
	if got := c.releaseFile(bookwarehouseSpec, "0.3.12", "plugin-linux-amd64"); got != want {
		t.Fatalf("release file = %q, want %q", got, want)
	}
	for _, host := range []string{"github.com", "release-assets.githubusercontent.com", "raw.githubusercontent.com"} {
		if !c.allowed(&url.URL{Scheme: "https", Host: host}) {
			t.Fatalf("default host %s not allowed", host)
		}
	}
	if len(c.hosts) != 3 {
		t.Fatalf("default hosts = %v", c.hosts)
	}
}

func TestNativeCatalogSourceFromEnv(t *testing.T) {
	source, err := NativeCatalogSourceFromEnv(envOf(map[string]string{
		NativeCatalogURLEnv:    " https://plugins.example.org/plugins.json ",
		NativeReleaseURLEnv:    "https://plugins.example.org/releases/",
		NativeDownloadHostsEnv: " Plugins.Example.org, ,cdn.example.org",
	}))
	if err != nil {
		t.Fatal(err)
	}
	c, err := source.parse()
	if err != nil {
		t.Fatal(err)
	}
	if c.catalogURL != "https://plugins.example.org/plugins.json" || len(c.hosts) != 2 {
		t.Fatalf("parsed source = %+v", c)
	}
	if got := c.releaseFile(s3Spec, "0.4.3", "manifest.json"); got != "https://plugins.example.org/releases/s3-v0.4.3/manifest.json" {
		t.Fatalf("release file = %q", got)
	}
}

func TestNativeCatalogSourceRejectsUnsafeSettings(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"plain http catalog":   {NativeCatalogURLEnv: "http://plugins.example.org/plugins.json", NativeDownloadHostsEnv: "plugins.example.org", NativeReleaseURLEnv: "https://plugins.example.org/releases"},
		"catalog off the list": {NativeCatalogURLEnv: "https://other.example.org/plugins.json", NativeDownloadHostsEnv: "plugins.example.org", NativeReleaseURLEnv: "https://plugins.example.org/releases"},
		"release off the list": {NativeCatalogURLEnv: "https://plugins.example.org/plugins.json", NativeDownloadHostsEnv: "plugins.example.org"},
		"catalog query":        {NativeCatalogURLEnv: "https://plugins.example.org/plugins.json?token=x", NativeDownloadHostsEnv: "plugins.example.org", NativeReleaseURLEnv: "https://plugins.example.org/releases"},
		"release credentials":  {NativeCatalogURLEnv: "https://plugins.example.org/plugins.json", NativeDownloadHostsEnv: "plugins.example.org", NativeReleaseURLEnv: "https://user:pass@plugins.example.org/releases"},
		"catalog directory":    {NativeCatalogURLEnv: "https://plugins.example.org/", NativeDownloadHostsEnv: "plugins.example.org", NativeReleaseURLEnv: "https://plugins.example.org/releases"},
		"host with a path":     {NativeDownloadHostsEnv: "github.com/evil"},
		"default catalog only": {NativeDownloadHostsEnv: "plugins.example.org"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NativeCatalogSourceFromEnv(envOf(env)); err == nil {
				t.Fatal("unsafe setting accepted")
			}
		})
	}
}

// ownHostFixture serves a catalog whose releases live on the configured host,
// and fails any request that leaves it.
func ownHostFixture(t *testing.T, version string, binary []byte, binaryURL string, requests *[]string) *http.Client {
	t.Helper()
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	manifest, err := protojson.Marshal(&publicv1.PluginManifest{PluginId: bookwarehouseSpec.PluginID, Version: version, SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}})
	if err != nil {
		t.Fatal(err)
	}
	inventory, _ := json.Marshal(map[string]any{"plugins": []any{map[string]any{"plugin_id": bookwarehouseSpec.PluginID, "source_version": version,
		"binaries": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]any{"url": binaryURL, "checksum": checksum}}}}})
	files := map[string][]byte{
		"/plugins.json": inventory,
		"/releases/bookwarehouse-v" + version + "/manifest.json":                                 manifest,
		"/releases/bookwarehouse-v" + version + "/plugin-" + runtime.GOOS + "-" + runtime.GOARCH: binary,
	}
	return &http.Client{Transport: storageCatalogTransport(func(r *http.Request) (*http.Response, error) {
		*requests = append(*requests, r.URL.String())
		body, ok := files[r.URL.Path]
		if r.URL.Host != "plugins.example.org" || !ok {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
}

func TestNativeCatalogReadsAndDownloadsFromConfiguredHost(t *testing.T) {
	binary := []byte("bookwarehouse binary")
	sum := sha256.Sum256(binary)
	checksum := hex.EncodeToString(sum[:])
	root := t.TempDir()
	var requests []string
	registry := ownHostRegistry(t, root)
	registry.catalogClient = ownHostFixture(t, "0.3.12", binary,
		"https://plugins.example.org/releases/bookwarehouse-v0.3.12/plugin-"+runtime.GOOS+"-"+runtime.GOARCH, &requests)
	key := catalogArtifactKey(bookwarehouseSpec, "0.3.12", checksum)
	got, err := registry.CatalogBinary(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatal("downloaded binary differs")
	}
	for _, r := range requests {
		if !strings.HasPrefix(r, "https://plugins.example.org/") {
			t.Fatalf("request left the configured host: %s", r)
		}
	}
	if len(requests) != 3 {
		t.Fatalf("want catalog, manifest and binary requests, got %v", requests)
	}
	if a := registry.ApprovedArtifacts(); len(a) != 1 || a[0].ArtifactKey != key {
		t.Fatalf("approved = %+v", a)
	}
}

// A server reading its own host refuses a catalog that still points releases
// at GitHub, as the default refuses any other host.
func TestNativeCatalogRefusesReleaseURLsOffTheConfiguredBase(t *testing.T) {
	var requests []string
	registry := ownHostRegistry(t, t.TempDir())
	registry.catalogClient = ownHostFixture(t, "0.3.12", []byte("x"),
		"https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.3.12/plugin-"+runtime.GOOS+"-"+runtime.GOARCH, &requests)
	if err := registry.RefreshCatalog(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid native catalog URL") {
		t.Fatalf("RefreshCatalog = %v, want invalid native catalog URL", err)
	}
}

func TestNativeCatalogRefusesHostsOffTheList(t *testing.T) {
	registry := ownHostRegistry(t, t.TempDir())
	called := false
	registry.catalogClient = &http.Client{Transport: storageCatalogTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}
	if _, err := registry.catalogRead(context.Background(), "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.3.12/plugin-linux-amd64", 1<<20); err == nil || called {
		t.Fatalf("request to a host off the list: err %v, sent %v", err, called)
	}
	if _, err := registry.catalogRead(context.Background(), "http://plugins.example.org/plugins.json", 1<<20); err == nil || called {
		t.Fatalf("plain http request: err %v, sent %v", err, called)
	}
	c := registry.catalogSource()
	if c.checkRedirect(&url.URL{Scheme: "https", Host: "release-assets.githubusercontent.com"}, 1) == nil {
		t.Fatal("redirect off the list accepted")
	}
	if c.checkRedirect(&url.URL{Scheme: "https", Host: "plugins.example.org"}, 1) != nil {
		t.Fatal("redirect on the list refused")
	}
	if c.checkRedirect(&url.URL{Scheme: "https", Host: "plugins.example.org"}, 6) == nil {
		t.Fatal("sixth redirect accepted")
	}
	if defaultNativeCatalogConfig.checkRedirect(&url.URL{Scheme: "https", Host: "release-assets.githubusercontent.com"}, 1) != nil {
		t.Fatal("default source refused GitHub's release asset host")
	}
}

// Moving the server to another host, or back, keeps every cached release
// admitted: an installed Bookwarehouse needs its record to start, and the
// catalog lists only the newest release. The record's download moves to the
// current source.
func TestNativeCatalogCacheSurvivesASourceChange(t *testing.T) {
	checksum := strings.Repeat("e", 64)
	m := &publicv1.PluginManifest{PluginId: bookwarehouseSpec.PluginID, Version: "0.3.11", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	key := catalogArtifactKey(bookwarehouseSpec, "0.3.11", checksum)
	file := "/bookwarehouse-v0.3.11/plugin-" + runtime.GOOS + "-" + runtime.GOARCH
	for name, tc := range map[string]struct {
		cachedURL string
		registry  func(*testing.T, string) *NativeStorageRegistry
		wantURL   string
	}{
		"to own host": {
			cachedURL: "https://github.com/Bloem-Studios/bloem-plugins/releases/download" + file,
			registry:  ownHostRegistry,
			wantURL:   "https://plugins.example.org/releases" + file,
		},
		"back to GitHub": {
			cachedURL: "https://plugins.example.org/releases" + file,
			registry: func(_ *testing.T, root string) *NativeStorageRegistry {
				return &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
			},
			wantURL: "https://github.com/Bloem-Studios/bloem-plugins/releases/download" + file,
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			record := cachedCatalogRecord(t, m, "bookwarehouse-v")
			record.URL = tc.cachedURL
			writeCatalogCache(t, root, map[string]storageCatalogRecord{key: record})
			registry := tc.registry(t, root)
			if err := registry.loadCatalog(); err != nil {
				t.Fatal(err)
			}
			if _, ok := registry.artifact(key); !ok {
				t.Fatal("cached release lost its admission")
			}
			if got := registry.catalogRecords[key].URL; got != tc.wantURL {
				t.Fatalf("cached download = %q, want %q", got, tc.wantURL)
			}
		})
	}
}

// A cached record must still name its own release; only the host and base may
// differ from the current source.
func TestNativeCatalogCacheStillSkipsAnotherReleasesFile(t *testing.T) {
	checksum := strings.Repeat("f", 64)
	m := &publicv1.PluginManifest{PluginId: bookwarehouseSpec.PluginID, Version: "0.3.11", SiloApiVersion: "v1", Checksum: checksum,
		SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	key := catalogArtifactKey(bookwarehouseSpec, "0.3.11", checksum)
	for name, cached := range map[string]string{
		"other version": "https://plugins.example.org/releases/bookwarehouse-v0.3.10/plugin-" + runtime.GOOS + "-" + runtime.GOARCH,
		"other plugin":  "https://plugins.example.org/releases/s3-v0.3.11/plugin-" + runtime.GOOS + "-" + runtime.GOARCH,
		"query":         "https://plugins.example.org/releases/bookwarehouse-v0.3.11/plugin-" + runtime.GOOS + "-" + runtime.GOARCH + "?x=1",
		"plain http":    "http://plugins.example.org/releases/bookwarehouse-v0.3.11/plugin-" + runtime.GOOS + "-" + runtime.GOARCH,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			record := cachedCatalogRecord(t, m, "bookwarehouse-v")
			record.URL = cached
			writeCatalogCache(t, root, map[string]storageCatalogRecord{key: record})
			registry := ownHostRegistry(t, root)
			if err := registry.loadCatalog(); err != nil {
				t.Fatal(err)
			}
			if _, ok := registry.artifact(key); ok {
				t.Fatal("record naming another file was admitted")
			}
		})
	}
}

func TestWithNativeCatalogSource(t *testing.T) {
	registry := &NativeStorageRegistry{baseDir: t.TempDir(), approved: make(map[string]NativeStorageArtifact)}
	if err := WithNativeCatalogSource(ownHostSource)(registry); err != nil {
		t.Fatal(err)
	}
	if registry.catalogSource().catalogURL != ownHostSource.CatalogURL {
		t.Fatal("option did not set the source")
	}
	if err := WithNativeCatalogSource(NativeCatalogSource{CatalogURL: "http://x/plugins.json"})(registry); err == nil {
		t.Fatal("invalid source accepted")
	}
}
