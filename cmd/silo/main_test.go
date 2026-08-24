package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-server/internal/api"
	"github.com/Silo-Server/silo-server/internal/compatgateway"
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

type staticWatchSyncCapabilityStore struct {
	capabilities []*plugins.Capability
}

func (s staticWatchSyncCapabilityStore) ListEnabled(context.Context) ([]*plugins.Installation, error) {
	return []*plugins.Installation{{ID: 4, Enabled: true, Kind: plugins.KindPlugin}}, nil
}

func (s staticWatchSyncCapabilityStore) ListCapabilities(context.Context, int) ([]*plugins.Capability, error) {
	return s.capabilities, nil
}

func TestReloadWatchSyncPluginProvidersPreservesConnectionForm(t *testing.T) {
	manifest := &pluginv1.PluginManifest{Capabilities: []*pluginv1.CapabilityDescriptor{{
		Type: "watch_sync_provider.v1", Id: "floppy", DisplayName: "Floppy",
		ConfigSchema: []*pluginv1.ConfigSchema{{
			Key: "floppy", Title: "Your Floppy server", Required: true,
			JsonSchema: `{"type":"object","properties":{"base_url":{"type":"string"}},"required":["base_url"]}`,
			AdminForm: &pluginv1.AdminFormDescriptor{Fields: []*pluginv1.AdminFormField{{
				Key: "base_url", Label: "Server URL", Required: true,
				Control: pluginv1.AdminFormControl_ADMIN_FORM_CONTROL_TEXT,
			}}},
		}},
		WatchSyncProvider: &pluginv1.WatchSyncProviderDescriptor{
			AuthMethods: []pluginv1.WatchSyncAuthMethod{pluginv1.WatchSyncAuthMethod_WATCH_SYNC_AUTH_METHOD_API_KEY},
		},
	}}}
	records, err := plugins.CapabilityRecordsFromManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := make([]*plugins.Capability, 0, len(records))
	for i := range records {
		record := records[i]
		capabilities = append(capabilities, &record)
	}

	registry := watchsync.NewRegistry()
	if err := reloadWatchSyncPluginProviders(
		context.Background(), registry, staticWatchSyncCapabilityStore{capabilities: capabilities}, &plugins.Service{}, nil,
	); err != nil {
		t.Fatal(err)
	}
	provider, ok := registry.Get("plugin:4:floppy")
	if !ok {
		t.Fatal("Floppy provider was not registered")
	}
	configurable, ok := provider.(interface {
		ConnectionConfigSchema() []plugins.ConfigSchemaView
	})
	if !ok {
		t.Fatal("Floppy provider does not expose connection configuration")
	}
	schemas := configurable.ConnectionConfigSchema()
	if len(schemas) != 1 || schemas[0].AdminForm == nil || len(schemas[0].AdminForm.Fields) != 1 ||
		schemas[0].AdminForm.Fields[0].Control != "TEXT" {
		t.Fatalf("connection config schema = %#v", schemas)
	}
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
// publicListenerCases is the shared probe table for the public listener:
// every layer, in the shapes real clients emit.
func publicListenerCases() []struct {
	path  string
	layer string
} {
	return []struct {
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
		// Native surfaces: the API tree, the SPA shell, and the reserved SPA
		// routes that share a name with a Jellyfin family.
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
}

// publicMux is what publicServer installs; this drives it directly so a
// composition fault is reported against the composition rather than the
// server wrapper.
func TestPublicMuxRoutesEachLayer(t *testing.T) {
	mux := publicMux(markerHandler{"api"}, markerHandler{"spa"}, markerHandler{"gateway"})
	for _, tc := range publicListenerCases() {
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

// publicServer is what servePublic installs on the public listener, so the
// test drives that function directly: a fault in the timeouts or the composed
// handler is reported here, against the builder, before the end-to-end test
// below reports it against the port.
func TestPublicServerRoutesEachLayer(t *testing.T) {
	srv := publicServer(":9999", markerHandler{"api"}, markerHandler{"spa"}, markerHandler{"gateway"})
	if srv.Addr != ":9999" {
		t.Fatalf("public server listens on %q", srv.Addr)
	}
	if srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 {
		t.Fatalf("public server must carry its timeouts, got %+v", srv)
	}
	for _, tc := range publicListenerCases() {
		rec := httptest.NewRecorder()
		srv.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Header().Get("X-Layer"); got != tc.layer {
			t.Fatalf("%s reached layer %q, want %q", tc.path, got, tc.layer)
		}
	}
}

// --- The public port, over a real socket -------------------------------------

// publicPortProbe drives the composed public port over real HTTP and reports
// which layer answered. The gateway layer is adjudicated by the response the
// *real* gateway gives when it owns a path and has no application behind it:
// 503, Retry-After, a trace header, and its own error code. That is a
// behavioral fingerprint no fallback handler produces, so "the gateway owns
// this path" is established by what came back rather than by what the wiring
// looks like.
func probePublicPort(t *testing.T, client *http.Client, base string, cases []struct {
	path  string
	layer string
}) {
	t.Helper()
	for _, tc := range cases {
		resp, err := client.Get(base + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read %s: %v", tc.path, readErr)
		}
		layer := resp.Header.Get("X-Layer")

		if tc.layer != "gateway" {
			if layer != tc.layer {
				t.Fatalf("%s was answered by %q, want %q (status %d)", tc.path, layer, tc.layer, resp.StatusCode)
			}
			continue
		}

		if layer != "" {
			t.Fatalf("%s was answered by the %q layer; the compatibility gateway owns it", tc.path, layer)
		}
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s answered %d, want %d from the gateway with no application enrolled",
				tc.path, resp.StatusCode, http.StatusServiceUnavailable)
		}
		if resp.Header.Get("Retry-After") == "" {
			t.Fatalf("%s carries no Retry-After; that is not the gateway answering", tc.path)
		}
		if resp.Header.Get("X-Bloem-Trace-Id") == "" {
			t.Fatalf("%s carries no gateway trace header; that is not the gateway answering", tc.path)
		}
		var payload struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("%s body is not the gateway's JSON error: %v (%q)", tc.path, err, string(body))
		}
		if payload.Error != "compatibility_unavailable" {
			t.Fatalf("%s answered error %q, want compatibility_unavailable", tc.path, payload.Error)
		}
	}
}

func publicProbeClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		// Redirects are part of what is being measured; following one would
		// hide which layer produced it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// startTestPublicPort runs the production composition root on an ephemeral
// loopback port and returns its base URL. Nothing is stubbed but the API
// router and the SPA: the listener, the server, the mux, and the gateway are
// the real ones main uses.
func startTestPublicPort(t *testing.T, gateway *compatgateway.Gateway) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("bind loopback listener: %v", err)
	}
	errCh := make(chan error, 1)
	port := servePublic(ln, markerHandler{"api"}, markerHandler{"spa"}, gateway, errCh)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if shutdownErr := port.shutdown(ctx); shutdownErr != nil {
			t.Errorf("public port shutdown: %v", shutdownErr)
		}
		select {
		case serveErr := <-errCh:
			t.Errorf("public port reported %v", serveErr)
		default:
		}
	})
	return "http://" + ln.Addr().String()
}

// This is the guarantee the source guards below only protect: the composition
// root binds a socket, serves the composed handler on it, and every layer
// answers on the wire. It drives servePublic — the one function main calls —
// with the real *compatgateway.Gateway over a real TCP connection, so a
// composition that is built and discarded, a handler swapped after the fact,
// or a gateway that never reaches the port shows up as a wrong HTTP response
// rather than as a passing structural check.
func TestServePublicServesTheComposedHandlerOverTheNetwork(t *testing.T) {
	gateway := compatgateway.New(compatgateway.Config{IdentitySecret: []byte("public-port-test-secret")})
	base := startTestPublicPort(t, gateway)
	client := publicProbeClient()

	probePublicPort(t, client, base, publicListenerCases())

	// /metrics is the composition's own, not any of the three layers'.
	resp, err := client.Get(base + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if layer := resp.Header.Get("X-Layer"); layer != "" {
		t.Fatalf("/metrics was answered by the %q layer; it must stay on the metrics handler", layer)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/metrics answered %d", resp.StatusCode)
	}
}

// A nil *compatgateway.Gateway must compose to the pre-gateway listener. The
// trap is that a typed nil satisfies http.Handler as a *non-nil* interface, so
// a servePublic that forwarded it straight through would route every owned
// family into a nil receiver instead of the SPA.
func TestServePublicWithoutGatewayServesTheSPA(t *testing.T) {
	base := startTestPublicPort(t, nil)
	client := publicProbeClient()

	for _, path := range []string{"/", "/System/Info", "/audiobookshelf/api/ping", "/web"} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		layer := resp.Header.Get("X-Layer")
		_ = resp.Body.Close()
		if layer != "spa" {
			t.Fatalf("%s was answered by %q, want spa when no gateway is configured", path, layer)
		}
	}
	resp, err := client.Get(base + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	layer := resp.Header.Get("X-Layer")
	_ = resp.Body.Close()
	if layer != "api" {
		t.Fatalf("/api/v1/health was answered by %q, want api", layer)
	}
}

// --- Source guard: nothing else in package main can serve the public port ----
//
// The behavioral test above proves what servePublic does. It cannot prove that
// main calls it, because main is not callable from a test. These guards close
// that gap, and they are resolved through go/types rather than identifier
// spelling: every earlier revision of this pin matched *names* — the receiver
// an alias happened to use, the substring "publicServer", the identifier a
// composed value was assigned to — and every one of them was defeated by
// renaming something. A type-resolved rule asks what a value *is*.

const (
	compatgatewayPkg = "github.com/Silo-Server/silo-server/internal/compatgateway"
	configPkg        = "github.com/Silo-Server/silo-server/internal/config"
	apiPkg           = "github.com/Silo-Server/silo-server/internal/api"
	serverPkg        = "github.com/Silo-Server/silo-server/internal/server"
)

// serverBuilders may compose an http.Server. Each owns exactly one listener
// and is individually reviewable; "somewhere inside main()" is not on the list
// and is precisely the place a rival listener used to be able to appear.
var serverBuilders = map[string]bool{
	"publicServer":          true,
	"absCompatServer":       true,
	"startStandaloneServer": true,
}

// serveCallers may put a handler on a socket.
var serveCallers = map[string]bool{
	"servePublic":           true,
	"serveAux":              true,
	"startStandaloneServer": true,
}

// listenBinders may open a listening socket.
var listenBinders = map[string]bool{"listenPublic": true}

// addressBinders may be handed the configured public address. Any other
// function in package main receiving it is trying to bind the public port
// behind the composition root's back.
var addressBinders = map[string]bool{"listenPublic": true, "startStandaloneServer": true}

// serveFuncs is every standard-library entry point that puts a handler on a
// socket, named by resolved type rather than by method name: a `Serve` method
// on something that is not an *http.Server is not one of these, and an
// *http.Server reached through any alias, field, or helper still is.
var serveFuncs = map[string]bool{
	"net/http.(*Server).Serve":             true,
	"net/http.(*Server).ServeTLS":          true,
	"net/http.(*Server).ListenAndServe":    true,
	"net/http.(*Server).ListenAndServeTLS": true,
	"net/http.Serve":                       true,
	"net/http.ServeTLS":                    true,
	"net/http.ListenAndServe":              true,
	"net/http.ListenAndServeTLS":           true,
}

// listenFuncs is every standard-library way to open a listening socket.
var listenFuncs = map[string]bool{
	"net.Listen":                       true,
	"net.ListenTCP":                    true,
	"net.ListenIP":                     true,
	"net.ListenUnix":                   true,
	"net.ListenUDP":                    true,
	"net.ListenPacket":                 true,
	"net.(*ListenConfig).Listen":       true,
	"net.(*ListenConfig).ListenPacket": true,
	"crypto/tls.Listen":                true,
	"crypto/tls.NewListener":           true,
}

// loadPackageMain type-checks package main. Types are the point: without them
// the guards below degrade to matching names, which is exactly the failure
// mode this revision exists to end. Type-checking is the expensive part of
// this file, so it happens once for the whole package's tests.
var (
	mainPackageOnce sync.Once
	mainPackage     *packages.Package
	mainPackageErr  error
)

func loadPackageMain(t *testing.T) *packages.Package {
	t.Helper()
	mainPackageOnce.Do(func() {
		mainPackage, mainPackageErr = typeCheckPackageMain()
	})
	if mainPackageErr != nil {
		t.Fatalf("type-check package main: %v", mainPackageErr)
	}
	return mainPackage
}

func typeCheckPackageMain() (*packages.Package, error) {
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedTypesSizes | packages.NeedSyntax | packages.NeedTypesInfo,
	}, ".")
	if err != nil {
		return nil, err
	}
	if len(pkgs) != 1 {
		return nil, fmt.Errorf("loaded %d packages, want exactly package main", len(pkgs))
	}
	pkg := pkgs[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("package main did not type-check: %v", pkg.Errors)
	}
	if pkg.Name != "main" {
		return nil, fmt.Errorf("loaded package %q, want main", pkg.Name)
	}
	if pkg.Types == nil || pkg.TypesInfo == nil {
		return nil, errors.New("no type information; this guard must not run on syntax alone")
	}
	// Every non-test file of package main, not just main.go: a listener stood
	// up in a sibling file would otherwise be invisible.
	if len(pkg.Syntax) < 2 {
		return nil, fmt.Errorf("type-checked only %d sources of package main; the scan is stale", len(pkg.Syntax))
	}
	return pkg, nil
}

// walkPackageMain visits every node of every non-test source, reporting the
// top-level declaration that contains it. Function literals are attributed to
// their enclosing declaration, so wrapping the offending code in a `go func()`
// or a closure does not launder it.
func walkPackageMain(pkg *packages.Package, visit func(owner string, stack []ast.Node)) {
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			owner := "a package-level declaration"
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = fn.Name.Name
			}
			var stack []ast.Node
			ast.Inspect(decl, func(node ast.Node) bool {
				if node == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, node)
				visit(owner, stack)
				return true
			})
		}
	}
}

// namedTypeIs reports whether typ (dereferenced once) is the named type
// pkgPath.name.
func namedTypeIs(typ types.Type, pkgPath, name string) bool {
	if typ == nil {
		return false
	}
	typ = types.Unalias(typ)
	if ptr, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(ptr.Elem())
	}
	named, ok := typ.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkgPath && obj.Name() == name
}

// callee resolves the function a call expression actually invokes.
func callee(info *types.Info, call *ast.CallExpr) *types.Func {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		fn, _ := info.Uses[fun].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := info.Uses[fun.Sel].(*types.Func)
		return fn
	}
	return nil
}

// funcID renders a resolved function as "net/http.(*Server).ListenAndServe" or
// "net.Listen" — an identity, not a spelling.
func funcID(fn *types.Func) string {
	if fn == nil {
		return ""
	}
	pkgPath := ""
	if fn.Pkg() != nil {
		pkgPath = fn.Pkg().Path()
	}
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		recv := types.Unalias(sig.Recv().Type())
		star := ""
		if ptr, ok := recv.(*types.Pointer); ok {
			recv = types.Unalias(ptr.Elem())
			star = "*"
		}
		if named, ok := recv.(*types.Named); ok {
			owner := named.Obj()
			ownerPkg := pkgPath
			if owner.Pkg() != nil {
				ownerPkg = owner.Pkg().Path()
			}
			return fmt.Sprintf("%s.(%s%s).%s", ownerPkg, star, owner.Name(), fn.Name())
		}
	}
	if pkgPath == "" {
		return fn.Name()
	}
	return pkgPath + "." + fn.Name()
}

func namesOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Only the named functions may build an http.Server, open a listening socket,
// or serve a handler — and nothing anywhere may replace a server's handler
// after it was composed. Each rule is stated over resolved types, so the
// evasions that beat the previous revisions are all one rule: rename the
// receiver, alias the server, wrap the call in a helper, spell the mux
// differently — the resolved type is unchanged and the rule still fires.
func TestOnlyBlessedFunctionsBuildBindOrServeTheHTTPStack(t *testing.T) {
	pkg := loadPackageMain(t)
	info := pkg.TypesInfo
	saw := map[string]int{}

	walkPackageMain(pkg, func(owner string, stack []ast.Node) {
		node := stack[len(stack)-1]
		where := pkg.Fset.Position(node.Pos())
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if !namedTypeIs(info.TypeOf(typed), "net/http", "Server") {
				return
			}
			saw["http.Server literal"]++
			if !serverBuilders[owner] {
				t.Errorf("%s composes an http.Server at %s; only %v may build one",
					owner, where, namesOf(serverBuilders))
			}
		case *ast.CallExpr:
			id := funcID(callee(info, typed))
			switch {
			case serveFuncs[id]:
				saw["serve call"]++
				if !serveCallers[owner] {
					t.Errorf("%s calls %s at %s; only %v may put a handler on a socket",
						owner, id, where, namesOf(serveCallers))
				}
			case listenFuncs[id]:
				saw["listen call"]++
				if !listenBinders[owner] {
					t.Errorf("%s calls %s at %s; only %v may bind a listener",
						owner, id, where, namesOf(listenBinders))
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range typed.Lhs {
				selector, ok := lhs.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Handler" {
					continue
				}
				if !namedTypeIs(info.TypeOf(selector.X), "net/http", "Server") {
					continue
				}
				t.Errorf("%s reassigns an http.Server's Handler at %s; a composed handler is installed once, by publicServer, and never replaced",
					owner, where)
			}
		}
	})

	// A guard that matches nothing passes for the wrong reason. Each rule must
	// have found at least one real instance of what it governs.
	for _, kind := range []string{"http.Server literal", "serve call", "listen call"} {
		if saw[kind] == 0 {
			t.Fatalf("the guard found no %s anywhere in package main; it is not seeing the sources", kind)
		}
	}
}

// soleCallTo returns the one call to a package-main function, failing if there
// is not exactly one.
func soleCallTo(t *testing.T, pkg *packages.Package, name string) *ast.CallExpr {
	t.Helper()
	var found []*ast.CallExpr
	walkPackageMain(pkg, func(_ string, stack []ast.Node) {
		call, ok := stack[len(stack)-1].(*ast.CallExpr)
		if !ok {
			return
		}
		fn := callee(pkg.TypesInfo, call)
		if fn == nil || fn.Pkg() != pkg.Types || fn.Name() != name {
			return
		}
		found = append(found, call)
	})
	if len(found) != 1 {
		t.Fatalf("package main calls %s %d times; it must have exactly one call site", name, len(found))
	}
	return found[0]
}

// The public port has one source. Counting resolved callees rather than
// assignment right-hand sides is what closes the wrapper hole: a helper that
// returns publicServer(...) from a `return` statement is still a call to
// publicServer, and routing main through such a helper leaves servePublic with
// zero call sites.
func TestBlessedListenerFunctionsHaveExactlyOneCallSite(t *testing.T) {
	pkg := loadPackageMain(t)

	// function -> the only declaration allowed to call it.
	wantCaller := map[string]string{
		"publicServer":          "servePublic",
		"servePublic":           "main",
		"listenPublic":          "main",
		"absCompatServer":       "main",
		"startStandaloneServer": "main",
	}

	callers := map[string][]string{}
	walkPackageMain(pkg, func(owner string, stack []ast.Node) {
		call, ok := stack[len(stack)-1].(*ast.CallExpr)
		if !ok {
			return
		}
		fn := callee(pkg.TypesInfo, call)
		if fn == nil || fn.Pkg() != pkg.Types {
			return
		}
		if _, tracked := wantCaller[fn.Name()]; !tracked {
			return
		}
		callers[fn.Name()] = append(callers[fn.Name()], owner)
	})

	for _, name := range namesOf(map[string]bool{
		"publicServer": true, "servePublic": true, "listenPublic": true,
		"absCompatServer": true, "startStandaloneServer": true,
	}) {
		got := callers[name]
		if len(got) != 1 {
			t.Fatalf("%s is called %d times in package main (from %v); it must have exactly one call site",
				name, len(got), got)
		}
		if got[0] != wantCaller[name] {
			t.Fatalf("%s is called from %s; its only call site is in %s", name, got[0], wantCaller[name])
		}
	}
}

// assignmentsTo collects every expression that gives obj a value anywhere in
// package main.
func assignmentsTo(pkg *packages.Package, obj types.Object) []ast.Expr {
	var sources []ast.Expr
	info := pkg.TypesInfo
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.AssignStmt:
				for i, lhs := range typed.Lhs {
					ident, ok := lhs.(*ast.Ident)
					if !ok || (info.Defs[ident] != obj && info.Uses[ident] != obj) {
						continue
					}
					switch {
					case len(typed.Rhs) == len(typed.Lhs):
						sources = append(sources, typed.Rhs[i])
					case len(typed.Rhs) == 1:
						sources = append(sources, typed.Rhs[0])
					}
				}
			case *ast.ValueSpec:
				for i, name := range typed.Names {
					if info.Defs[name] != obj {
						continue
					}
					switch {
					case i < len(typed.Values):
						sources = append(sources, typed.Values[i])
					case len(typed.Values) == 1:
						sources = append(sources, typed.Values[0])
					}
				}
			}
			return true
		})
	}
	return sources
}

// requireValueFrom resolves an argument to the call that produced it — the
// expression itself, or, for an identifier, the single assignment that gave it
// its value — and requires that call to be the named function. Two assignments
// is a failure rather than a tie-break: "composed, then replaced" is exactly
// how a correct composition gets thrown away.
func requireValueFrom(t *testing.T, pkg *packages.Package, label string, arg ast.Expr, wantPkg, wantFunc string) {
	t.Helper()

	expr := ast.Unparen(arg)
	if call, ok := expr.(*ast.CallExpr); ok {
		requireCallee(t, pkg, label, call, wantPkg, wantFunc)
		return
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("the %s argument is %s, which is neither a call nor an identifier; this guard cannot see where it came from",
			label, types.ExprString(arg))
	}
	obj := pkg.TypesInfo.Uses[ident]
	if obj == nil {
		t.Fatalf("cannot resolve the %s argument %q", label, ident.Name)
	}
	if _, isNil := obj.(*types.Nil); isNil {
		t.Fatalf("the %s argument is nil; the public port must be composed with the real %s.%s", label, wantPkg, wantFunc)
	}
	sources := assignmentsTo(pkg, obj)
	if len(sources) != 1 {
		t.Fatalf("the %s argument %q is given a value %d times in package main; it must have exactly one source",
			label, ident.Name, len(sources))
	}
	call, ok := ast.Unparen(sources[0]).(*ast.CallExpr)
	if !ok {
		t.Fatalf("the %s argument %q is not produced by a call but by %s",
			label, ident.Name, types.ExprString(sources[0]))
	}
	requireCallee(t, pkg, label, call, wantPkg, wantFunc)
}

func requireCallee(t *testing.T, pkg *packages.Package, label string, call *ast.CallExpr, wantPkg, wantFunc string) {
	t.Helper()
	fn := callee(pkg.TypesInfo, call)
	if fn == nil {
		t.Fatalf("cannot resolve the callee behind the %s argument (%s)", label, types.ExprString(call))
	}
	path := ""
	if fn.Pkg() != nil {
		path = fn.Pkg().Path()
	}
	if path != wantPkg || fn.Name() != wantFunc {
		t.Fatalf("the %s argument comes from %s.%s, want %s.%s", label, path, fn.Name(), wantPkg, wantFunc)
	}
}

// The one servePublic call must be handed the real dependencies. Counting
// calls was never enough — servePublic(ln, router, frontend, nil) is one call
// with the gateway switched off — so every argument is resolved back to the
// constructor that produced it.
func TestPublicPortIsComposedFromTheRealDependencies(t *testing.T) {
	pkg := loadPackageMain(t)

	// The gateway parameter is the concrete type on purpose: with every
	// parameter an http.Handler, swapping the frontend and the gateway type-
	// checked and silently moved the whole compatibility surface off the port.
	obj := pkg.Types.Scope().Lookup("servePublic")
	if obj == nil {
		t.Fatal("package main has no servePublic")
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		t.Fatalf("servePublic is not a function, it is %s", obj.Type())
	}
	if sig.Params().Len() != 5 {
		t.Fatalf("servePublic takes %d parameters; this guard pins the 5-parameter form", sig.Params().Len())
	}
	if _, isPointer := types.Unalias(sig.Params().At(0).Type()).(*types.Pointer); isPointer {
		t.Fatal("servePublic's first parameter must stay the net.Listener interface")
	}
	if !types.Implements(sig.Params().At(0).Type(), listenerInterface(t, pkg)) {
		t.Fatalf("servePublic's first parameter is %s, want net.Listener: the bind has to happen inside the composition root",
			sig.Params().At(0).Type())
	}
	gatewayParam := sig.Params().At(3).Type()
	if _, isPointer := types.Unalias(gatewayParam).(*types.Pointer); !isPointer ||
		!namedTypeIs(gatewayParam, compatgatewayPkg, "Gateway") {
		t.Fatalf("servePublic's gateway parameter is %s; it must stay *compatgateway.Gateway so an argument swap cannot compile",
			gatewayParam)
	}

	call := soleCallTo(t, pkg, "servePublic")
	if len(call.Args) != 5 {
		t.Fatalf("servePublic is called with %d arguments, want 5", len(call.Args))
	}
	requireValueFrom(t, pkg, "listener", call.Args[0], pkg.PkgPath, "listenPublic")
	requireValueFrom(t, pkg, "API router", call.Args[1], apiPkg, "NewRouter")
	requireValueFrom(t, pkg, "frontend", call.Args[2], serverPkg, "FrontendHandler")
	requireValueFrom(t, pkg, "gateway", call.Args[3], compatgatewayPkg, "New")

	// The auxiliary listener must not be pointed at the public address either.
	abs := soleCallTo(t, pkg, "absCompatServer")
	if len(abs.Args) != 2 {
		t.Fatalf("absCompatServer is called with %d arguments, want 2", len(abs.Args))
	}
	if got := types.ExprString(abs.Args[0]); !strings.HasSuffix(got, ".AudiobookshelfCompat.Listen") {
		t.Fatalf("absCompatServer binds %s; it may only bind the Audiobookshelf-compat address", got)
	}
}

func listenerInterface(t *testing.T, pkg *packages.Package) *types.Interface {
	t.Helper()
	netPkg := pkg.Imports["net"]
	if netPkg == nil || netPkg.Types == nil {
		t.Fatal("package main does not import net; it cannot bind its own listener")
	}
	obj := netPkg.Types.Scope().Lookup("Listener")
	if obj == nil {
		t.Fatal("net.Listener not found")
	}
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		t.Fatalf("net.Listener is %s, not an interface", obj.Type())
	}
	return iface
}

// The public port is an address, not a variable name. Every mention of the
// configured public address in package main is checked: it may be logged,
// formatted into a node URL, or assigned — but the moment it is handed to a
// function of this package, that function must be one that is allowed to bind
// it. Copying it into a local first does not help, because the local's own
// mention is what gets checked at the call.
func TestConfiguredPublicAddressIsBoundOnlyByTheBlessedListener(t *testing.T) {
	pkg := loadPackageMain(t)
	mentions := 0

	walkPackageMain(pkg, func(owner string, stack []ast.Node) {
		selector, ok := stack[len(stack)-1].(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Listen" {
			return
		}
		if !namedTypeIs(pkg.TypesInfo.TypeOf(selector.X), configPkg, "ServerConfig") {
			return
		}
		mentions++
		if len(stack) < 2 {
			return
		}
		call, ok := stack[len(stack)-2].(*ast.CallExpr)
		if !ok {
			return
		}
		isArgument := false
		for _, arg := range call.Args {
			if ast.Unparen(arg) == ast.Expr(selector) {
				isArgument = true
			}
		}
		if !isArgument {
			return
		}
		fn := callee(pkg.TypesInfo, call)
		// Only calls into package main are governed: handing the address to
		// slog or fmt does not bind anything.
		if fn == nil || fn.Pkg() != pkg.Types {
			return
		}
		if !addressBinders[fn.Name()] {
			t.Errorf("%s passes the configured public address to %s at %s; only %v may be given the public port",
				owner, fn.Name(), pkg.Fset.Position(selector.Pos()), namesOf(addressBinders))
		}
	})

	if mentions < 2 {
		t.Fatalf("found %d mentions of the configured public address; the scan is stale", mentions)
	}
}
