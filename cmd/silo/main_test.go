package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/playback"
	"github.com/Silo-Server/silo-server/internal/plugins"
	"github.com/Silo-Server/silo-server/internal/watchsync"
)

func TestConfigureS3Clients_SetsCORSOnPublicAssetsBucket(t *testing.T) {
	publicServer := newS3BucketRecorder(t)

	cfg := &config.Config{
		S3: config.S3Config{
			Public: config.S3PublicAssetsSettings{
				S3BucketSettings: config.S3BucketSettings{
					Endpoint:  publicServer.URL(),
					Region:    "us-east-1",
					Bucket:    "public-assets",
					AccessKey: "test",
					SecretKey: "test",
					PathStyle: true,
				},
			},
		},
	}

	deps := &api.Dependencies{}
	configureS3Clients(cfg, deps)

	if deps.S3Public == nil {
		t.Fatal("S3Public should be configured")
	}
	if got := publicServer.CORSRequests(); got != 1 {
		t.Fatalf("public assets bucket CORS requests = %d, want 1", got)
	}
}

type lifecycleWorkerStub struct {
	started chan struct{}
	stopped chan struct{}
}

func (w *lifecycleWorkerStub) Run(ctx context.Context) {
	close(w.started)
	<-ctx.Done()
	close(w.stopped)
}

func TestStartAdminPeopleBackgroundWorkerOwnsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &lifecycleWorkerStub{started: make(chan struct{}), stopped: make(chan struct{})}

	startAdminPeopleBackgroundWorker(ctx, worker)
	select {
	case <-worker.started:
	case <-time.After(time.Second):
		t.Fatal("admin people worker did not start")
	}

	cancel()
	select {
	case <-worker.stopped:
	case <-time.After(time.Second):
		t.Fatal("admin people worker did not stop with application context")
	}
}

func TestConfigureS3Clients_PassesPublicKeyPrefix(t *testing.T) {
	publicServer := newS3BucketRecorder(t)

	cfg := &config.Config{
		S3: config.S3Config{
			Public: config.S3PublicAssetsSettings{
				S3BucketSettings: config.S3BucketSettings{
					Endpoint:  publicServer.URL(),
					Region:    "us-east-1",
					Bucket:    "public-assets",
					KeyPrefix: "silo/dev",
					AccessKey: "test",
					SecretKey: "test",
					PathStyle: true,
				},
			},
		},
	}

	deps := &api.Dependencies{}
	configureS3Clients(cfg, deps)

	if deps.S3Public == nil {
		t.Fatal("S3Public should be configured")
	}

	url, err := deps.S3Public.PublicURL(deps.S3Public.Bucket(), "catalog-seeds/export.json.gz")
	if err != nil {
		t.Fatalf("PublicURL() returned error: %v", err)
	}
	if !strings.Contains(url, "/silo/dev/catalog-seeds/export.json.gz") {
		t.Fatalf("PublicURL() = %q, want prefixed path", url)
	}
}

type s3BucketRecorder struct {
	server       *httptest.Server
	mu           sync.Mutex
	corsRequests int
}

func newS3BucketRecorder(t *testing.T) *s3BucketRecorder {
	t.Helper()

	recorder := &s3BucketRecorder{}
	recorder.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		if r.Method == http.MethodPut && r.URL.Query().Has("cors") {
			recorder.mu.Lock()
			recorder.corsRequests++
			recorder.mu.Unlock()
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(recorder.server.Close)

	return recorder
}

func (r *s3BucketRecorder) URL() string {
	return r.server.URL
}

func (r *s3BucketRecorder) CORSRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.corsRequests
}

func TestBuildLiveSessionSync_UsesTransportPlayMethod(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		session playback.Session
		want    string
	}{
		{
			name: "transcode transport remains transcode when base method is remux",
			session: playback.Session{
				ID:                   "session-1",
				UserID:               7,
				ProfileID:            "profile-1",
				MediaFileID:          42,
				RequestedMediaFileID: 41,
				PlayMethod:           playback.PlayTranscode,
				BasePlayMethod:       playback.PlayRemux,
				TranscodeHWAccel:     "qsv",
				Position:             125.5,
				IsPaused:             true,
			},
			want: "transcode",
		},
		{
			name: "remux transport stays remux",
			session: playback.Session{
				ID:                   "session-2",
				UserID:               8,
				ProfileID:            "profile-2",
				MediaFileID:          99,
				RequestedMediaFileID: 99,
				PlayMethod:           playback.PlayRemux,
				BasePlayMethod:       playback.PlayRemux,
			},
			want: "remux",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := buildLiveSessionSync(&tc.session, "node-a")
			if got.PlayMethod != tc.want {
				t.Fatalf("PlayMethod = %q, want %q", got.PlayMethod, tc.want)
			}
			if got.ReportingNode != "node-a" {
				t.Fatalf("ReportingNode = %q, want %q", got.ReportingNode, "node-a")
			}
			if got.SessionID != tc.session.ID {
				t.Fatalf("SessionID = %q, want %q", got.SessionID, tc.session.ID)
			}
			if got.ProfileID != tc.session.ProfileID {
				t.Fatalf("ProfileID = %q, want %q", got.ProfileID, tc.session.ProfileID)
			}
			if got.PositionSeconds != tc.session.Position {
				t.Fatalf("PositionSeconds = %v, want %v", got.PositionSeconds, tc.session.Position)
			}
			if got.IsPaused != tc.session.IsPaused {
				t.Fatalf("IsPaused = %v, want %v", got.IsPaused, tc.session.IsPaused)
			}
			if got.TranscodeHWAccel != tc.session.TranscodeHWAccel {
				t.Fatalf("TranscodeHWAccel = %q, want %q", got.TranscodeHWAccel, tc.session.TranscodeHWAccel)
			}
		})
	}
}

type failingWatchSyncCapabilityStore struct{}

func (failingWatchSyncCapabilityStore) ListEnabled(context.Context) ([]*plugins.Installation, error) {
	return []*plugins.Installation{{ID: 2, Enabled: true, Kind: plugins.KindPlugin}}, nil
}

func (failingWatchSyncCapabilityStore) ListCapabilities(context.Context, int) ([]*plugins.Capability, error) {
	return nil, errors.New("database unavailable")
}

func TestReloadWatchSyncPluginProvidersDropsStaleProvidersOnCapabilityReadFailure(t *testing.T) {
	registry := watchsync.NewRegistry()
	provider, err := watchsync.NewPluginProvider(watchsync.PluginProviderOptions{
		InstallationID: 1,
		ProviderKey:    "plugin:1:tracker",
		CapabilityID:   "tracker",
		Descriptor: &pluginv1.WatchSyncProviderDescriptor{
			AuthMethods:   []pluginv1.WatchSyncAuthMethod{pluginv1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY},
			ExportWatched: true,
		},
		ResolveClient: func(context.Context, int, string) (watchsync.WatchSyncPluginClient, error) {
			return nil, errors.New("not used")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}

	if err := reloadWatchSyncPluginProviders(
		context.Background(), registry, failingWatchSyncCapabilityStore{}, &plugins.Service{}, nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get(provider.Key()); ok {
		t.Fatalf("stale provider %q remained registered", provider.Key())
	}
}

// --- Public listener composition --------------------------------------------

// markerHandler answers with the name of the layer that received a request,
// so the composition can be adjudicated by response rather than by reading
// the wiring.
type markerHandler struct{ name string }

func (m markerHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Layer", m.name)
	w.WriteHeader(http.StatusOK)
}

// publicMux is what the production listener serves. This drives the real
// function main() uses, so a change to the composition — reverting the
// gateway ahead of the SPA fallback, say — fails here rather than shipping.
func TestPublicMuxRoutesEachLayer(t *testing.T) {
	mux := publicMux(markerHandler{"api"}, markerHandler{"spa"}, markerHandler{"gateway"})

	cases := []struct {
		path  string
		layer string
	}{
		// Jellyfin protocol families, in every shape real clients emit.
		{"/System/Info", "gateway"},
		{"/system/info", "gateway"},
		{"/emby/System/Info", "gateway"},
		{"/jellyfin/System/Info", "gateway"},
		{"/Users/AuthenticateByName", "gateway"},
		{"/web/index.html", "gateway"},
		// Audiobookshelf's single family.
		{"/audiobookshelf/api/ping", "gateway"},
		// Native surfaces: the API tree, metrics, the SPA shell, and the
		// reserved SPA routes that share a name with a Jellyfin family.
		{"/api/v1/health", "api"},
		{"/api/v1/auth/login", "api"},
		{"/api/v2/admin/session", "api"},
		{"/api/internal/compat/v1/identity", "api"},
		{"/search", "spa"},
		{"/library/5", "spa"},
		{"/livetv", "spa"},
		{"/", "spa"},
		{"/assets/app.js", "spa"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Header().Get("X-Layer"); got != tc.layer {
			t.Fatalf("%s reached layer %q, want %q", tc.path, got, tc.layer)
		}
	}

	// /metrics is served by the composition itself, not by any of the three.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if layer := rec.Header().Get("X-Layer"); layer != "" {
		t.Fatalf("/metrics reached layer %q; it must stay on the metrics handler", layer)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics answered %d", rec.Code)
	}
}

// Without a gateway the composition is byte-identical to the pre-gateway
// listener: everything outside /api/** and /metrics is the SPA's.
func TestPublicMuxWithoutGateway(t *testing.T) {
	mux := publicMux(markerHandler{"api"}, markerHandler{"spa"}, nil)
	for _, path := range []string{"/", "/System/Info", "/audiobookshelf/api/ping", "/web"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("X-Layer"); got != "spa" {
			t.Fatalf("%s reached layer %q, want spa", path, got)
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if got := rec.Header().Get("X-Layer"); got != "api" {
		t.Fatalf("/api/v1/health reached layer %q, want api", got)
	}
}
