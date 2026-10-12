package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/bookrequests"
	"github.com/Silo-Server/silo-server/internal/pluginhost"
	"github.com/Silo-Server/silo-server/internal/requests"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/protobuf/encoding/protojson"
)

// This optional cross-repository gate starts the real plugin executable. Every
// downloader operation targets the fixture, and adoption avoids source queries.
func TestAudiobookRouterRealPluginAdoptsWithoutSubmitting(t *testing.T) {
	binary := os.Getenv("BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY")
	if binary == "" {
		t.Skip("BLOEM_AUDIOBOOK_REQUEST_PLUGIN_BINARY not set")
	}
	submission := "12345678-1234-1234-1234-123456789abc"
	var mutations atomic.Int32
	downloader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/app/webapiVersion":
			_, _ = w.Write([]byte("2.11.0"))
		case "/api/v2/torrents/info":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"hash": "0123456789012345678901234567890123456789", "name": "Synthetic Saga", "state": "downloading", "tags": "bloem-" + submission + "-a1", "progress": 0.5, "size": 100000000, "total_size": 100000000, "save_path": "/downloads", "category": "bloem-audiobooks"}})
		default:
			mutations.Add(1)
			http.Error(w, "unexpected downloader operation", http.StatusInternalServerError)
		}
	}))
	defer downloader.Close()
	host := pluginhost.NewHost(pluginhost.Config{Logger: hclog.NewNullLogger()})
	defer func() { _ = host.Shutdown(context.Background()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	manifestJSON, err := exec.CommandContext(ctx, binary, "manifest").Output()
	if err != nil {
		t.Fatalf("read acceptance plugin manifest: %v", err)
	}
	manifest := &pluginv1.PluginManifest{}
	if err = protojson.Unmarshal(manifestJSON, manifest); err != nil {
		t.Fatal(err)
	}

	process, err := host.Start(ctx, pluginhost.StartRequest{InstallationID: 7, BinaryPath: binary, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	client, err := process.RequestRouter("audiobooks")
	if err != nil {
		t.Fatal(err)
	}
	installation := 7
	adapter := bloemAudiobookRouter{integrations: ebookConnectionFixture{&requests.Integration{ID: "connection", Enabled: true, InstallationID: &installation, CapabilityID: "audiobooks", SupportedMediaTypes: []string{"audiobook"}, APIKeyRef: "abk1.e30", PluginConfig: map[string]any{"qbittorrent_url": downloader.URL, "qbittorrent_save_root": "/downloads", "allow_http_download_clients": true, "allow_loopback_targets": true, "nzbking_enabled": false}}}, resolver: ebookResolverFixture{client}, validator: audiobookValidatorFixture{}}
	in := bookrequests.AudiobookDispatchInput{Request: bookrequests.Request{ID: submission, MediaType: "audiobook", Title: "Synthetic Saga", Author: "Test Author"}, Integration: bookrequests.AudiobookIntegration{ID: "connection", InstallationID: installation, CapabilityID: "audiobooks", Language: "en"}, SubmissionID: submission, Attempt: 1, SearchPass: 1}
	out, err := adapter.Fulfill(ctx, in)
	if err != nil || out.Kind != bookrequests.AudiobookHandedOff || out.Decision == nil || out.Decision.Outcome != "adopted" {
		t.Fatalf("host decode %+v %v", out, err)
	}
	receipt, err := bookrequests.ParseAudiobookReceipt(out.Receipt.ExternalID, submission, 1)
	if err != nil || receipt.Kind != "qbittorrent" || !receipt.Adopted || mutations.Load() != 0 {
		t.Fatalf("adoption receipt=%+v mutations=%d err=%v", receipt, mutations.Load(), err)
	}
	status, err := adapter.CheckStatus(ctx, in, out.Receipt)
	if err != nil || status.Kind != bookrequests.AudiobookStatus || status.ProviderStatus != "downloading" || mutations.Load() != 0 {
		t.Fatalf("status %+v mutations=%d %v", status, mutations.Load(), err)
	}
}
