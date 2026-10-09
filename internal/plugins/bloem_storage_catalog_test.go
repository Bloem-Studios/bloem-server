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

func TestNativeCatalogAdmitsWithoutManualApprovalAndPersists(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	manifest := &publicv1.PluginManifest{PluginId: "bloem.storage.bookwarehouse", Version: "0.2.2", SiloApiVersion: "v1", Checksum: checksum, SupportedPlatforms: []*publicv1.SupportedPlatform{{Os: runtime.GOOS, Arch: runtime.GOARCH}}}
	raw, err := protojson.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.2.2/plugin-" + runtime.GOOS + "-" + runtime.GOARCH
	inventory, _ := json.Marshal(map[string]any{"plugins": []any{map[string]any{"plugin_id": manifest.PluginId, "source_version": "0.2.2", "binaries": map[string]any{runtime.GOOS + "/" + runtime.GOARCH: map[string]any{"url": url, "checksum": checksum}}}}})
	client := &http.Client{Transport: storageCatalogTransport(func(r *http.Request) (*http.Response, error) {
		body := inventory
		if strings.HasSuffix(r.URL.Path, "manifest.json") {
			body = raw
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	root := t.TempDir()
	registry := &NativeStorageRegistry{baseDir: root, approved: make(map[string]NativeStorageArtifact)}
	registry.catalogClient = client
	if err := registry.RefreshCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CatalogBinary(context.Background(), catalogArtifactKey("0.2.2", checksum)); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	artifacts := registry.ApprovedArtifacts()
	if len(artifacts) != 1 || artifacts[0].PluginID != "bloem.storage.bookwarehouse" {
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

func TestNativeCatalogRejectsExternalBinaryURLs(t *testing.T) {
	if storageCatalogURL("https://attacker.example/plugin", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("external URL accepted")
	}
	if storageCatalogURL("https://github.com/Bloem-Studios/bloem-plugins/releases/download/bookwarehouse-v0.2.2/plugin-linux-amd64?token=secret", "0.2.2", "plugin-linux-amd64") {
		t.Fatal("query accepted")
	}
}
