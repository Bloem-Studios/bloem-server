package compatgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStates is the test double for the narrow application-state view the
// gateway consumes. It stands in for the enrollment/trust service.
type fakeStates struct {
	mu       sync.Mutex
	statuses map[AppKind]Status
	err      error
}

func (f *fakeStates) ApplicationStatus(_ context.Context, kind AppKind) (Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Status{}, f.err
	}
	status, ok := f.statuses[kind]
	if !ok {
		return Status{}, nil
	}
	return status, nil
}

func (f *fakeStates) set(kind AppKind, status Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statuses == nil {
		f.statuses = map[AppKind]Status{}
	}
	f.statuses[kind] = status
}

func availableStatus(endpoint *url.URL) Status {
	return Status{
		Known:         true,
		Enrolled:      true,
		Enabled:       true,
		Healthy:       true,
		APICompatible: true,
		Endpoint:      endpoint,
	}
}

// recordingTransport captures the exact request the gateway would send
// upstream and answers with a canned response.
type recordingTransport struct {
	mu       sync.Mutex
	requests []*http.Request
	respond  func(r *http.Request) (*http.Response, error)
}

func (t *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.requests = append(t.requests, r.Clone(context.Background()))
	respond := t.respond
	t.mu.Unlock()
	if respond != nil {
		return respond(r)
	}
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusOK)
	return rec.Result(), nil
}

func (t *recordingTransport) recorded() []*http.Request {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*http.Request(nil), t.requests...)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return parsed
}

func newTestGateway(t *testing.T, states StateProvider, transport http.RoundTripper) *Gateway {
	t.Helper()
	return New(Config{
		States:         states,
		Transport:      transport,
		IdentitySecret: []byte("gateway-test-secret"),
	})
}

// --- Static route table -----------------------------------------------------

// The gateway owns a compile-time route table. Native surfaces — the Vondel
// application at "/", /api/v1/**, /api/v2/**, and /metrics — must never appear
// in it, and no gateway prefix may shadow them.
func TestRouteTableNeverClaimsNativeSurfaces(t *testing.T) {
	reserved := []string{"/", "/api", "/metrics"}
	for _, route := range RouteTable() {
		if route.Prefix == "" || !strings.HasPrefix(route.Prefix, "/") {
			t.Fatalf("route prefix %q must be absolute", route.Prefix)
		}
		if strings.HasSuffix(route.Prefix, "/") {
			t.Fatalf("route prefix %q must not end in a slash", route.Prefix)
		}
		for _, native := range reserved {
			if route.Prefix == native || strings.HasPrefix(route.Prefix+"/", native+"/") && native != "/" {
				t.Fatalf("route prefix %q claims native surface %q", route.Prefix, native)
			}
		}
		if route.Prefix == "/" {
			t.Fatalf("gateway must not claim the native application root")
		}
	}
}

// Native authentication is never replaced: no gateway route may sit on or
// under the native auth route families.
func TestRouteTableNeverReplacesNativeAuthRoutes(t *testing.T) {
	nativeAuth := []string{"/api/v1/auth", "/api/v2/admin/session", "/api/v1/profiles"}
	for _, route := range RouteTable() {
		for _, native := range nativeAuth {
			if route.Prefix == native || strings.HasPrefix(native, route.Prefix+"/") {
				t.Fatalf("gateway route %q would shadow native auth route %q", route.Prefix, native)
			}
		}
	}
}

func TestRouteTableOwnership(t *testing.T) {
	byPrefix := map[string]Route{}
	for _, route := range RouteTable() {
		if _, dup := byPrefix[route.Prefix]; dup {
			t.Fatalf("duplicate route prefix %q", route.Prefix)
		}
		byPrefix[route.Prefix] = route
	}
	abs, ok := byPrefix["/audiobookshelf"]
	if !ok || abs.App != KindAudiobookshelf || !abs.StripPrefix {
		t.Fatalf("audiobookshelf must own /audiobookshelf with a stripped prefix, got %+v", abs)
	}
	for prefix, route := range byPrefix {
		if prefix == "/audiobookshelf" {
			continue
		}
		if route.App != KindJellyfin {
			t.Fatalf("only jellyfin and /audiobookshelf are owned, got %+v", route)
		}
		if route.StripPrefix {
			t.Fatalf("jellyfin routes keep their protocol paths, got %+v", route)
		}
	}
	for _, required := range []string{"/System", "/Users", "/Items", "/Sessions", "/LiveTv", "/web"} {
		if _, ok := byPrefix[required]; !ok {
			t.Fatalf("jellyfin fixed route set must include %q", required)
		}
	}
}

// The route table is static configuration: mutating a returned copy must not
// change what the gateway matches. A companion cannot add a route at runtime.
func TestRouteTableIsImmutable(t *testing.T) {
	table := RouteTable()
	for i := range table {
		table[i].Prefix = "/api/v1/auth"
		table[i].App = KindJellyfin
	}
	if _, ok := MatchPath("/api/v1/auth/login"); ok {
		t.Fatal("mutating the returned route table changed gateway matching")
	}
	if _, ok := MatchPath("/System/Info/Public"); !ok {
		t.Fatal("route table lost its static entries")
	}
}

func TestMatchPath(t *testing.T) {
	cases := []struct {
		path string
		want AppKind
		ok   bool
	}{
		{"/System/Info/Public", KindJellyfin, true},
		// Ownership is exact-case. On the shared canonical origin a
		// case-insensitive claim would shadow the native SPA routes /search,
		// /library, and /livetv behind /Search, /Library, and /LiveTv.
		// Official Jellyfin clients emit the canonical casing; lowercase
		// probes fall through to the SPA.
		{"/system/info/public", "", false},
		{"/Users/AuthenticateByName", KindJellyfin, true},
		{"/web/index.html", KindJellyfin, true},
		{"/web", KindJellyfin, true},
		{"/audiobookshelf", KindAudiobookshelf, true},
		{"/audiobookshelf/api/ping", KindAudiobookshelf, true},
		{"/", "", false},
		{"/api/v1/items", "", false},
		{"/api/v2/admin/session", "", false},
		{"/metrics", "", false},
		{"/SystemX", "", false},                 // prefix match is per segment, not per string
		{"/audiobookshelfx/api", "", false},     // no partial-segment ownership
		{"/Audiobookshelf/api/ping", "", false}, // the ABS prefix is exact
	}
	for _, tc := range cases {
		route, ok := MatchPath(tc.path)
		if ok != tc.ok || (ok && route.App != tc.want) {
			t.Fatalf("MatchPath(%q) = (%+v, %v), want app %q ok %v", tc.path, route, ok, tc.want, tc.ok)
		}
	}
}

// --- Availability -----------------------------------------------------------

// Requests the table does not own never fall through to a companion, no
// matter what state the applications are in.
func TestUnownedPathsNeverProxy(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://vondel-audiobookshelf:13378")))
	gateway := newTestGateway(t, states, transport)

	for _, path := range []string{"/", "/api/v1/auth/login", "/api/v1/items", "/api/v2/organizations", "/metrics", "/unknown"} {
		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q: got status %d, want 404", path, rec.Code)
		}
	}
	if len(transport.recorded()) != 0 {
		t.Fatalf("unowned paths reached the upstream transport: %d requests", len(transport.recorded()))
	}
}

// A container that is merely running claims no routes: until the application
// is enrolled, enabled, healthy, and API-compatible, its paths answer
// compatibility_unavailable and nothing reaches an endpoint.
func TestUnavailableStatesDoNotProxy(t *testing.T) {
	endpoint := mustParseURL(t, "http://vondel-jellyfin:8096")
	cases := map[string]Status{
		"unknown":      {},
		"unenrolled":   {Known: true, Endpoint: endpoint},
		"disabled":     {Known: true, Enrolled: true, Healthy: true, APICompatible: true, Endpoint: endpoint},
		"revoked":      {Known: true, Enrolled: true, Enabled: true, Revoked: true, Healthy: true, APICompatible: true, Endpoint: endpoint},
		"unhealthy":    {Known: true, Enrolled: true, Enabled: true, APICompatible: true, Endpoint: endpoint},
		"incompatible": {Known: true, Enrolled: true, Enabled: true, Healthy: true, Endpoint: endpoint},
		"no endpoint":  {Known: true, Enrolled: true, Enabled: true, Healthy: true, APICompatible: true},
	}
	for name, status := range cases {
		transport := &recordingTransport{}
		states := &fakeStates{}
		states.set(KindJellyfin, status)
		gateway := newTestGateway(t, states, transport)

		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: got status %d, want 503", name, rec.Code)
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "compatibility_unavailable" {
			t.Fatalf("%s: body %q must carry compatibility_unavailable", name, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("%s: unavailable responses advertise Retry-After", name)
		}
		if len(transport.recorded()) != 0 {
			t.Fatalf("%s: an unavailable application must not be dialed", name)
		}
	}
}

// A state-service failure fails closed.
func TestStateErrorFailsClosed(t *testing.T) {
	transport := &recordingTransport{}
	gateway := newTestGateway(t, &fakeStates{err: errors.New("store down")}, transport)
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/api/ping", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", rec.Code)
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("a failed state lookup must not be proxied")
	}
}

// --- Proxy behavior ---------------------------------------------------------

func TestProxyStripsHopByHopAndSignsIdentity(t *testing.T) {
	transport := &recordingTransport{
		respond: func(*http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Connection", "keep-alive")
			rec.Header().Set("Keep-Alive", "timeout=5")
			rec.Header().Set("X-Companion", "yes")
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	secret := []byte("gateway-test-secret")
	gateway := New(Config{States: states, Transport: transport, IdentitySecret: secret})

	req := httptest.NewRequest(http.MethodGet, "/System/Info", nil)
	req.Header.Set("Connection", "close")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Upgrade", "h2c")
	req.Header.Set("Proxy-Authorization", "Basic evil")
	req.Header.Set("Trailer", "Expires")
	// A spoofed internal identity must never survive to the companion.
	req.Header.Set("X-Vondel-Internal-Identity", "forged")
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, req)

	upstream := transport.recorded()
	if len(upstream) != 1 {
		t.Fatalf("expected exactly one upstream request, got %d", len(upstream))
	}
	sent := upstream[0]
	for _, header := range []string{"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade", "Proxy-Authorization", "Trailer"} {
		if sent.Header.Get(header) != "" {
			t.Fatalf("hop-by-hop header %s leaked upstream as %q", header, sent.Header.Get(header))
		}
	}

	identity := sent.Header.Get("X-Vondel-Internal-Identity")
	if identity == "" || identity == "forged" {
		t.Fatalf("upstream identity %q must be gateway-signed, never caller-supplied", identity)
	}
	payload, signature, found := strings.Cut(identity, ".")
	if !found {
		t.Fatalf("identity %q must be payload.signature", identity)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	if !hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature)) {
		t.Fatal("identity signature does not verify against the shared secret")
	}
	if !strings.Contains(payload, string(KindJellyfin)) {
		t.Fatalf("identity payload %q must bind the application kind", payload)
	}

	trace := sent.Header.Get("X-Vondel-Trace-Id")
	if trace == "" {
		t.Fatal("upstream request must carry a trace id")
	}
	if rec.Header().Get("X-Vondel-Trace-Id") != trace {
		t.Fatal("the client response must echo the same trace id")
	}
	if rec.Header().Get("Keep-Alive") != "" {
		t.Fatal("hop-by-hop response headers must be stripped")
	}
	if rec.Header().Get("X-Companion") != "yes" {
		t.Fatal("end-to-end response headers must survive")
	}
}

func TestAudiobookshelfPrefixIsStripped(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://vondel-audiobookshelf:13378")))
	gateway := newTestGateway(t, states, transport)

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/api/ping?x=1", nil))
	upstream := transport.recorded()
	if len(upstream) != 1 {
		t.Fatalf("expected one upstream request, got %d", len(upstream))
	}
	if got := upstream[0].URL.Path; got != "/api/ping" {
		t.Fatalf("upstream path %q, want /api/ping", got)
	}
	if got := upstream[0].URL.RawQuery; got != "x=1" {
		t.Fatalf("upstream query %q, want x=1", got)
	}
	if got := upstream[0].URL.Host; got != "vondel-audiobookshelf:13378" {
		t.Fatalf("upstream host %q", got)
	}
}

func TestJellyfinPathIsForwardedVerbatim(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := newTestGateway(t, states, transport)

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/Users/AuthenticateByName", nil))
	upstream := transport.recorded()
	if len(upstream) != 1 || upstream[0].URL.Path != "/Users/AuthenticateByName" {
		t.Fatalf("jellyfin protocol paths must be forwarded unmodified, got %+v", upstream)
	}
}

// --- Limits -----------------------------------------------------------------

func TestRequestBodyLimit(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := New(Config{
		States:          states,
		Transport:       transport,
		IdentitySecret:  []byte("s"),
		MaxRequestBytes: 16,
	})

	req := httptest.NewRequest(http.MethodPost, "/Sessions/Playing", strings.NewReader(strings.Repeat("x", 64)))
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got status %d, want 413", rec.Code)
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("an oversized request must not reach the companion")
	}
}

func TestUpstreamDeadline(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	transport := &recordingTransport{
		respond: func(r *http.Request) (*http.Response, error) {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-release:
				rec := httptest.NewRecorder()
				rec.WriteHeader(http.StatusOK)
				return rec.Result(), nil
			}
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := New(Config{
		States:          states,
		Transport:       transport,
		IdentitySecret:  []byte("s"),
		UpstreamTimeout: 20 * time.Millisecond,
	})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not enforce the upstream deadline")
	}
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("got status %d, want 504", rec.Code)
	}
}

// --- Circuit breaking and isolation -----------------------------------------

// Repeated transport failures open the application's circuit: its own paths
// answer unavailable without dialing, while the other application keeps
// proxying. Failure never cascades into native handlers or the sibling.
func TestCircuitBreakerIsolatesTheFailingApplication(t *testing.T) {
	var mu sync.Mutex
	jellyfinDials := 0
	transport := &recordingTransport{
		respond: func(r *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if r.URL.Host == "vondel-jellyfin:8096" {
				jellyfinDials++
				return nil, errors.New("connection refused")
			}
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://vondel-audiobookshelf:13378")))
	gateway := New(Config{
		States:           states,
		Transport:        transport,
		IdentitySecret:   []byte("s"),
		FailureThreshold: 3,
		CircuitCooldown:  time.Hour,
	})

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("failure %d: got status %d, want 502", i, rec.Code)
		}
	}

	mu.Lock()
	dialsAtOpen := jellyfinDials
	mu.Unlock()
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("open circuit: got status %d, want 503", rec.Code)
	}
	mu.Lock()
	if jellyfinDials != dialsAtOpen {
		mu.Unlock()
		t.Fatal("an open circuit must not dial the companion")
	}
	mu.Unlock()

	// The sibling application is unaffected.
	rec = httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/api/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("audiobookshelf must keep serving while jellyfin's circuit is open, got %d", rec.Code)
	}
}

func TestCircuitClosesAfterCooldown(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var mu sync.Mutex
	fail := true
	transport := &recordingTransport{
		respond: func(*http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			if fail {
				return nil, errors.New("connection refused")
			}
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := New(Config{
		States:           states,
		Transport:        transport,
		IdentitySecret:   []byte("s"),
		FailureThreshold: 1,
		CircuitCooldown:  30 * time.Second,
		Now:              func() time.Time { return now },
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rec.Code)
	}
	rec = httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("circuit should be open, got %d", rec.Code)
	}

	mu.Lock()
	fail = false
	mu.Unlock()
	now = now.Add(31 * time.Second)
	rec = httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("circuit should close after the cooldown, got %d", rec.Code)
	}
}

// --- Redirects --------------------------------------------------------------

func TestRedirectsRemainSameOrigin(t *testing.T) {
	transport := &recordingTransport{
		respond: func(r *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			switch {
			case strings.HasPrefix(r.URL.Path, "/absolute"):
				rec.Header().Set("Location", "http://vondel-audiobookshelf:13378/login")
			case strings.HasPrefix(r.URL.Path, "/relative"):
				rec.Header().Set("Location", "/login")
			default:
				rec.Header().Set("Location", "https://evil.example/phish")
			}
			rec.WriteHeader(http.StatusFound)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://vondel-audiobookshelf:13378")))
	gateway := newTestGateway(t, states, transport)

	// An absolute redirect to the private endpoint is rewritten onto the
	// canonical origin, with the public prefix restored.
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/absolute", nil))
	if got := rec.Header().Get("Location"); got != "/audiobookshelf/login" {
		t.Fatalf("absolute upstream redirect became %q, want /audiobookshelf/login", got)
	}

	// A relative redirect gets the public prefix restored.
	rec = httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/relative", nil))
	if got := rec.Header().Get("Location"); got != "/audiobookshelf/login" {
		t.Fatalf("relative upstream redirect became %q, want /audiobookshelf/login", got)
	}

	// A redirect off-origin is refused rather than forwarded.
	rec = httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/offsite", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("off-origin redirect must be refused, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); strings.Contains(got, "evil.example") {
		t.Fatalf("off-origin location %q leaked to the client", got)
	}
}

// --- Composed public root (gateway ahead of the SPA fallback) ---------------

// spaMarker stands in for the Vondel SPA fallback and records what reached it.
type spaMarker struct {
	mu    sync.Mutex
	paths []string
}

func (s *spaMarker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.paths = append(s.paths, r.URL.Path)
	s.mu.Unlock()
	w.Header().Set("X-Handler", "spa")
	w.WriteHeader(http.StatusOK)
}

// The production listener hands only /api/** to the chi router; everything
// else goes through this composition. Gateway families must be answered by
// the gateway, and every native path — the SPA shell, its assets, and the
// lowercase client routes that sit next to Jellyfin's canonical-cased
// families — must still reach the SPA.
func TestWithFrontendFallbackSplitsOwnership(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://vondel-audiobookshelf:13378")))
	gateway := newTestGateway(t, states, transport)
	spa := &spaMarker{}
	composed := WithFrontendFallback(gateway, spa)

	gatewayPaths := []string{"/System/Info", "/audiobookshelf/api/ping", "/web", "/web/index.html", "/Users/AuthenticateByName"}
	for _, path := range gatewayPaths {
		rec := httptest.NewRecorder()
		composed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("X-Handler") == "spa" {
			t.Fatalf("gateway family path %q reached the SPA", path)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("gateway family path %q answered %d through the composition", path, rec.Code)
		}
	}
	if len(transport.recorded()) != len(gatewayPaths) {
		t.Fatalf("expected %d proxied requests, got %d", len(gatewayPaths), len(transport.recorded()))
	}

	// The /web-vs-SPA adjudication: /web belongs to the gateway (Jellyfin
	// Web), the SPA shell at "/" and everything else native stays SPA —
	// including the lowercase SPA routes an insensitive claim would swallow,
	// and lowercase Jellyfin probes, which are not part of the reviewed set.
	spaPaths := []string{"/", "/index.html", "/assets/app.js", "/search", "/library/5", "/livetv", "/admin/settings", "/system/info/public", "/Web"}
	for _, path := range spaPaths {
		rec := httptest.NewRecorder()
		composed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("X-Handler") != "spa" {
			t.Fatalf("native path %q did not reach the SPA (status %d)", path, rec.Code)
		}
	}
	if len(transport.recorded()) != len(gatewayPaths) {
		t.Fatal("a native path was proxied to a companion")
	}
}

// With no application routable, owned families answer unavailable — a
// missing application only ever takes down its own paths — while the SPA
// keeps serving everything native.
func TestWithFrontendFallbackWhenNothingIsRoutable(t *testing.T) {
	transport := &recordingTransport{}
	gateway := newTestGateway(t, &fakeStates{}, transport)
	spa := &spaMarker{}
	composed := WithFrontendFallback(gateway, spa)

	for _, path := range []string{"/System/Info", "/audiobookshelf/api/ping", "/web"} {
		rec := httptest.NewRecorder()
		composed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("owned path %q answered %d, want 503", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	composed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search", nil))
	if rec.Header().Get("X-Handler") != "spa" {
		t.Fatal("native paths must stay on the SPA while companions are absent")
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("nothing routable, nothing dialed")
	}
}

// A nil gateway composes to the plain SPA: deployments without the
// compatibility stack keep today's behavior exactly.
func TestWithFrontendFallbackNilGateway(t *testing.T) {
	spa := &spaMarker{}
	composed := WithFrontendFallback(nil, spa)
	for _, path := range []string{"/", "/System/Info", "/audiobookshelf/api/ping", "/web"} {
		rec := httptest.NewRecorder()
		composed.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("X-Handler") != "spa" {
			t.Fatalf("path %q must fall through to the SPA when no gateway exists", path)
		}
	}
}

// A Location of /\evil.example/phish carries no Host, so it survives a
// host-only check, but browsers normalize the backslash to // and leave the
// origin. It must be refused like any other off-origin redirect.
func TestBackslashRedirectIsRefused(t *testing.T) {
	transport := &recordingTransport{
		respond: func(*http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.Header().Set("Location", `/\evil.example/phish`)
			rec.WriteHeader(http.StatusFound)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := newTestGateway(t, states, transport)

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("backslash redirect must be refused, got %d", rec.Code)
	}
	if location := rec.Header().Get("Location"); strings.Contains(location, "evil.example") {
		t.Fatalf("backslash location %q leaked to the client", location)
	}
}

// A chunked request (no Content-Length) that overruns the body limit is cut
// off mid-stream and answered 413, not forwarded truncated.
func TestChunkedRequestBodyLimit(t *testing.T) {
	transport := &recordingTransport{
		respond: func(r *http.Request) (*http.Response, error) {
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				return nil, err
			}
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://vondel-jellyfin:8096")))
	gateway := New(Config{
		States:          states,
		Transport:       transport,
		IdentitySecret:  []byte("s"),
		MaxRequestBytes: 16,
	})

	req := httptest.NewRequest(http.MethodPost, "/Sessions/Playing", nil)
	req.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", 4096)))
	req.ContentLength = -1 // chunked: the length is unknown up front
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got status %d, want 413", rec.Code)
	}
}
