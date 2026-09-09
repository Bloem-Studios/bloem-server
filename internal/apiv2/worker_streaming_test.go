package apiv2

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/nodeconfig"
	"github.com/Silo-Server/silo-server/internal/routeinventory"
	"github.com/Silo-Server/silo-server/internal/streamtoken"
	"github.com/Silo-Server/silo-server/internal/transcodenode"
)

func TestWorkerDeliveryInventoryDescriptionCoverage(t *testing.T) {
	inventory, err := routeinventory.LoadArtifact(".")
	if err != nil {
		t.Fatal(err)
	}
	described := map[string]bool{}
	for _, op := range describeWorkerProtocols().Operations {
		described[op.Listener+" "+op.Method+" "+op.Path] = true
	}
	count := 0
	for _, route := range inventory.Routes {
		if route.Listener != "proxy" && route.Listener != "transcode_node" {
			continue
		}
		switch route.Path {
		case "/health", "/ready", "/metrics", "/api/v1/health":
			continue
		}
		count++
		if !described[route.Listener+" "+route.Method+" "+route.Path] {
			t.Fatal("undescribed worker registration", route.Listener, route.Method, route.Path)
		}
	}
	if count != 40 || len(described) != 40 {
		t.Fatal("retained inventory drift", count, len(described))
	}
}

func TestWorkerStreamingRefusalDescription(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "synthetic-streaming-worker"
	cfg.Playback.TranscodeDir = t.TempDir()
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	watcher.SetConfigForTest(cfg)
	handler := transcodenode.NewServer(watcher, nil).Handler()
	for _, op := range transcodenode.ProtocolStreaming() {
		path := strings.ReplaceAll(strings.ReplaceAll(op.Path, "{session_id}", "missing"), "{name}", "seg_00001.ts")
		for _, authenticated := range []bool{false, true} {
			req := httptest.NewRequest(op.Method, path, nil)
			if authenticated {
				req.Header.Set("Authorization", "Bearer "+cfg.Auth.JWTSecret)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			expected := 401
			if authenticated && !strings.HasPrefix(path, "/remux/") {
				expected = 404
			}
			if response.Code != expected {
				t.Fatal(op.Path, response.Code, response.Body)
			}
			if op.Responses[strconv.Itoa(expected)] == nil {
				t.Fatal("missing refusal")
			}
		}
	}
	// Proxy manifest HEAD is forwarded unchanged to a worker that registers GET
	// only. The existing node therefore refuses it; do not promise HEAD support.
	req := httptest.NewRequest(http.MethodHead, "/transcode/missing/master.m3u8", nil)
	req.Header.Set("Authorization", "Bearer "+cfg.Auth.JWTSecret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 405 {
		t.Fatal("node manifest HEAD behavior changed", response.Code)
	}
}

// Public viewer credentials must not become credentials for the trusted
// worker listener, even when the signed media recipe is also presented.
func TestWorkerStreamingRejectsViewerCredentials(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "worker-viewer-boundary-test"
	cfg.Playback.TranscodeDir = t.TempDir()
	watcher := nodeconfig.NewWatcher(nil, nil, nil, nodeconfig.BootstrapOverrides{})
	watcher.SetConfigForTest(cfg)
	h := transcodenode.NewServer(watcher, nil).Handler()
	accessToken, err := auth.NewJWTService(cfg.Auth.JWTSecret, time.Hour, time.Hour).GenerateAccessToken(7, "user", "login")
	if err != nil {
		t.Fatal(err)
	}
	mediaToken, err := streamtoken.Sign(streamtoken.Claims{UserID: 7, MediaFileID: 42, SessionID: "missing", PlayMethod: "direct"}, cfg.Auth.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range transcodenode.ProtocolStreaming() {
		path := strings.ReplaceAll(strings.ReplaceAll(op.Path, "{session_id}", "missing"), "{name}", "seg_00001.ts")
		for _, credential := range []string{accessToken, mediaToken} {
			r := httptest.NewRequest(op.Method, path, nil)
			r.Header.Set("Authorization", "Bearer "+credential)
			r.Header.Set("X-Silo-Stream-Token", mediaToken)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, r)
			if response.Code != 401 {
				t.Fatalf("%s %s accepted viewer credential: %d", op.Method, op.Path, response.Code)
			}
		}
	}
}
