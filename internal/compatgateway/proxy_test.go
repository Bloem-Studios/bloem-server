package compatgateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

// The gateway owns a compile-time route table. Native surfaces — the Bloem
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
	// The legacy prefixes the embedded listener strips are the only stripped
	// Jellyfin families; every protocol family keeps its own path.
	legacyStripped := map[string]bool{"/emby": true, "/jellyfin": true}
	for prefix, route := range byPrefix {
		if prefix == "/audiobookshelf" {
			continue
		}
		if route.App != KindJellyfin {
			t.Fatalf("only jellyfin and /audiobookshelf are owned, got %+v", route)
		}
		if route.StripPrefix != legacyStripped[prefix] {
			t.Fatalf("route %+v has the wrong strip behavior", route)
		}
	}
	for _, required := range []string{"/System", "/Users", "/Items", "/Sessions", "/LiveTv", "/web", "/emby", "/jellyfin"} {
		if _, ok := byPrefix[required]; !ok {
			t.Fatalf("jellyfin fixed route set must include %q", required)
		}
	}
}

// The reserved native segments are exactly the SPA's own first path segments
// that would otherwise be swallowed by a same-named Jellyfin family. This
// pins the set against the SPA's real route list, so a future lowercase SPA
// route colliding with a family fails here instead of disappearing behind
// the gateway.
func TestReservedNativeSegmentsCoverTheSPARoutes(t *testing.T) {
	// An object route table would declare paths this scan cannot see at all.
	// The SPA does not use one today; if that changes, the pin must be
	// rewritten rather than silently bypassed.
	if table := spaObjectRouterFiles(t); len(table) > 0 {
		t.Fatalf("SPA builds an object route table in %v; this pin only reads <Route> elements", table)
	}

	elements, files := spaRouteElements(t)
	if len(elements) < 20 || files == 0 {
		t.Fatalf("found %d <Route> elements across %d files; the scan is stale", len(elements), files)
	}
	families := map[string]bool{}
	for _, route := range RouteTable() {
		families[strings.ToLower(strings.TrimPrefix(route.Prefix, "/"))] = true
	}

	literal := regexp.MustCompile(`\spath="([^"]*)"`)
	// Any spelling that names a path, readable or not.
	declaresPath := regexp.MustCompile(`[\s{]path\s*[:=]`)
	spread := regexp.MustCompile(`\{\s*\.\.\.`)
	seen := map[string]bool{}
	caseSensitive := map[string]bool{}
	relative := 0
	for _, element := range elements {
		// A route whose path this scan cannot read is a failure, not a gap.
		// The rule is stated positively: if the tag mentions a path at all —
		// in any spelling, including single quotes, braces, backticks, or
		// extra whitespace — it must be one this pin can read. Silently
		// continuing when the literal pattern misses is what let
		// path={CONST} and path='/genres' through before.
		if declaresPath.MatchString(element) && !literal.MatchString(element) {
			t.Fatalf("SPA route declares a path this pin cannot read: %s", condense(element))
		}
		// A spread can carry a path the scan never sees at all.
		if spread.MatchString(element) {
			t.Fatalf("SPA route spreads props, hiding its path from this pin: %s", condense(element))
		}
		match := literal.FindStringSubmatch(element)
		if match == nil {
			continue
		}
		// Only an absolute path contributes a first URL segment. A relative
		// nested path ("users" under "/admin/*") resolves beneath its
		// ancestor, so its first segment is the ancestor's — already checked
		// here in its own right.
		if !strings.HasPrefix(match[1], "/") {
			relative++
			continue
		}
		segment := firstSegment(match[1])
		if segment == "" || segment == "*" || strings.HasPrefix(segment, ":") {
			continue
		}
		seen[segment] = true
		// Recorded per element, not per segment: a second /search route
		// without caseSensitive would otherwise ride on the first one's.
		//
		// The prop has to be *on*, not merely mentioned. A substring test
		// counted caseSensitive={false} as satisfying the reservation, which
		// is the exact opposite of what it asks for: React Router would then
		// match /LiveTv in the SPA while the gateway routes every non-
		// lowercase casing to Jellyfin, and the two halves of one origin
		// disagree about who owns the path.
		if reservedNativeSegments[segment] {
			switch {
			case caseSensitiveOff.MatchString(element):
				t.Fatalf("SPA route %q sets caseSensitive={false}; the reservation is lowercase-exact and this hands every other casing to both owners at once: %s",
					segment, condense(element))
			case !caseSensitiveMentioned.MatchString(element):
				t.Fatalf("SPA route %q must set caseSensitive on every declaration: %s", segment, condense(element))
			case !caseSensitiveOn.MatchString(element):
				t.Fatalf("SPA route %q sets caseSensitive to a value this pin cannot read as enabled; write it bare or as {true}: %s",
					segment, condense(element))
			}
		}
		if caseSensitiveOn.MatchString(element) {
			caseSensitive[segment] = true
		}
		if families[strings.ToLower(segment)] && !reservedNativeSegments[segment] {
			t.Fatalf("SPA route segment %q collides with a gateway family and is not reserved", segment)
		}
	}

	// The scan must be seeing the whole tree, nested routes included: the
	// admin section alone contributes dozens of relative children, and a
	// scanner that stops at the first container would report none.
	if relative < 10 {
		t.Fatalf("scan found only %d relative nested routes; it is not walking the whole tree", relative)
	}

	// The reserved set must not grow beyond what the SPA actually serves: a
	// stale entry silently withholds a family from the gateway.
	for segment := range reservedNativeSegments {
		if !seen[segment] {
			t.Fatalf("reserved segment %q is not an SPA route any more", segment)
		}
		if !families[strings.ToLower(segment)] {
			t.Fatalf("reserved segment %q collides with no gateway family; reserving it is pointless", segment)
		}
		// Reservation is lowercase-exact, so the SPA route must be too.
		// React Router matches case-insensitively by default, which would
		// have the SPA claim /Search while the gateway routes it to Jellyfin
		// — the two halves of one origin disagreeing about who owns a path.
		if !caseSensitive[segment] {
			t.Fatalf("SPA route %q must set caseSensitive: the gateway reserves only the lowercase form", segment)
		}
	}
}

// caseSensitive route-prop spellings. The prop is only satisfied when it is
// written bare or as {true}; anything else — {false}, a variable, an
// expression — is either a refusal or a value this pin cannot read, and both
// are failures rather than passes.
var (
	caseSensitiveMentioned = regexp.MustCompile(`(?:^|[\s{])caseSensitive\b`)
	caseSensitiveOn        = regexp.MustCompile(`(?:^|[\s{])caseSensitive\s*(?:=\s*\{\s*true\s*\})?(?:$|[\s/>])`)
	caseSensitiveOff       = regexp.MustCompile(`(?:^|[\s{])caseSensitive\s*=\s*\{\s*false\s*\}`)
)

// spaObjectRouterFiles reports files declaring a data-router route table,
// whose paths are object keys rather than <Route> attributes.
func spaObjectRouterFiles(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, file := range spaSourceFiles(t) {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, builder := range []string{"createBrowserRouter", "createHashRouter", "createMemoryRouter", "useRoutes("} {
			if strings.Contains(string(source), builder) {
				found = append(found, filepath.Base(file))
				break
			}
		}
	}
	return found
}

func condense(element string) string {
	return strings.Join(strings.Fields(element), " ")
}

// spaRouteElements returns every <Route …> opening tag in web/src, so a route
// declared outside App.tsx cannot escape the pin. Brace depth is tracked so
// an element={<Page />} attribute does not end the tag early.
func spaRouteElements(t *testing.T) ([]string, int) {
	t.Helper()
	var elements []string
	files := 0
	for _, file := range spaSourceFiles(t) {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		found := routeOpeningTags(string(source))
		if len(found) > 0 {
			files++
			elements = append(elements, found...)
		}
	}
	return elements, files
}

// spaSourceExtensions is every extension the SPA build accepts a module from.
// The walk used to see only .ts/.tsx, so a route declared in a .jsx file — or
// in one of the ESM/CJS TypeScript variants — was invisible to the pin.
var spaSourceExtensions = map[string]bool{
	".ts": true, ".tsx": true,
	".js": true, ".jsx": true,
	".mts": true, ".cts": true,
	".mjs": true, ".cjs": true,
}

// spaSourceFiles lists every non-test SPA source under web/src, so a route
// declared outside App.tsx cannot escape the pin.
func spaSourceFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	root := filepath.Join("..", "..", "web", "src")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.Contains(entry.Name(), ".test.") {
			return nil
		}
		if !spaSourceExtensions[filepath.Ext(path)] {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatalf("scan SPA sources: %v", err)
	}
	if len(files) < 50 {
		t.Fatalf("scan found only %d SPA sources; the walk is stale", len(files))
	}
	return files
}

func routeOpeningTags(source string) []string {
	var tags []string
	for idx := 0; idx < len(source); {
		offset := strings.Index(source[idx:], "<Route")
		if offset < 0 {
			break
		}
		start := idx + offset
		next := start + len("<Route")
		idx = next
		// "<Routes>" is the container element and "<RouteAnnouncer />" is a
		// component; neither declares a path.
		if next < len(source) && isIdentifierByte(source[next]) {
			continue
		}
		depth, end := 0, -1
		for i := next; i < len(source) && end < 0; i++ {
			switch source[i] {
			case '{':
				depth++
			case '}':
				depth--
			case '>':
				if depth == 0 {
					end = i
				}
			case '<':
				if strings.HasPrefix(source[i:], "<Route") {
					end = i
				}
			}
		}
		if end < 0 {
			end = len(source)
		}
		tags = append(tags, source[start:end])
	}
	return tags
}

func isIdentifierByte(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
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
		// The listener this replaces canonicalizes case, so real clients
		// reach these families in any casing. Ownership follows suit.
		{"/system/info/public", KindJellyfin, true},
		{"/SYSTEM/Info", KindJellyfin, true},
		{"/users/authenticatebyname", KindJellyfin, true},
		{"/items/5", KindJellyfin, true},
		{"/Users/AuthenticateByName", KindJellyfin, true},
		{"/web/index.html", KindJellyfin, true},
		{"/web", KindJellyfin, true},
		{"/Web", KindJellyfin, true},
		// The legacy /emby and /jellyfin prefixes the embedded listener
		// strips are owned families in their own right.
		{"/emby/System/Info", KindJellyfin, true},
		{"/jellyfin/System/Info", KindJellyfin, true},
		{"/emby/Users/AuthenticateByName", KindJellyfin, true},
		{"/EMBY/System/Info", KindJellyfin, true},
		{"/audiobookshelf", KindAudiobookshelf, true},
		{"/audiobookshelf/api/ping", KindAudiobookshelf, true},
		{"/Audiobookshelf/api/ping", KindAudiobookshelf, true},
		// Reserved native segments win over a family that differs only in
		// case: these are live SPA client routes on the same origin.
		{"/search", "", false},
		{"/library/5", "", false},
		{"/livetv", "", false},
		{"/", "", false},
		{"/api/v1/items", "", false},
		{"/api/v2/admin/session", "", false},
		{"/metrics", "", false},
		{"/SystemX", "", false},             // prefix match is per segment, not per string
		{"/audiobookshelfx/api", "", false}, // no partial-segment ownership
		{"/embyx/System", "", false},
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
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
	endpoint := mustParseURL(t, "http://bloem-jellyfin:8096")
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
	req.Header.Set("X-Bloem-Internal-Identity", "forged")
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

	identity := sent.Header.Get("X-Bloem-Internal-Identity")
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

	trace := sent.Header.Get("X-Bloem-Trace-Id")
	if trace == "" {
		t.Fatal("upstream request must carry a trace id")
	}
	if rec.Header().Get("X-Bloem-Trace-Id") != trace {
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
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
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
	if got := upstream[0].URL.Host; got != "bloem-audiobookshelf:13378" {
		t.Fatalf("upstream host %q", got)
	}
}

func TestJellyfinPathIsForwardedVerbatim(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
			if r.URL.Host == "bloem-jellyfin:8096" {
				jellyfinDials++
				return nil, errors.New("connection refused")
			}
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			return rec.Result(), nil
		},
	}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
				rec.Header().Set("Location", "http://bloem-audiobookshelf:13378/login")
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
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
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

// spaMarker stands in for the Bloem SPA fallback and records what reached it.
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
	states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
	gateway := newTestGateway(t, states, transport)
	spa := &spaMarker{}
	composed := WithFrontendFallback(gateway, spa)

	// The probe table covers every shape the embedded listener answers: the
	// canonical casing, the lowercase and shouted forms it canonicalizes,
	// and the two legacy prefixes it strips.
	gatewayPaths := []string{
		"/System/Info", "/audiobookshelf/api/ping", "/web", "/web/index.html",
		"/Users/AuthenticateByName",
		"/system/info", "/SYSTEM/Info",
		"/emby/System/Info", "/jellyfin/System/Info", "/emby/Users/AuthenticateByName",
	}
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
	// Web), while the SPA shell at "/" and everything else native stays SPA
	// — including the three reserved lowercase segments that would otherwise
	// be swallowed by the /Search, /Library, and /LiveTv families.
	spaPaths := []string{"/", "/index.html", "/assets/app.js", "/search", "/library/5", "/livetv", "/admin/settings", "/profiles", "/settings/playback"}
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
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

// --- Path encoding across the strip ----------------------------------------

// Stripping must operate on the escaped path as well as the decoded one.
// Rewriting only URL.Path and clearing RawPath re-encodes the request line
// from the decoded form, which turns an encoded slash into a real separator
// and hands the companion a path its client never asked for.
func TestStrippedFamiliesPreservePathEncoding(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"legacy emby prefix", "/emby/Items/a%2Fb/Images", "/Items/a%2Fb/Images"},
		{"legacy jellyfin prefix", "/jellyfin/Items/a%2Fb/Images", "/Items/a%2Fb/Images"},
		{"audiobookshelf prefix", "/audiobookshelf/api/items/a%2Fb", "/api/items/a%2Fb"},
		{"unstripped family", "/Items/a%2Fb/Images", "/Items/a%2Fb/Images"},
		{"plain path keeps working", "/emby/System/Info", "/System/Info"},
		{"encoded space", "/emby/Items/a%20b", "/Items/a%20b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &recordingTransport{}
			states := &fakeStates{}
			states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
			states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
			gateway := newTestGateway(t, states, transport)

			rec := httptest.NewRecorder()
			gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			upstream := transport.recorded()
			if len(upstream) != 1 {
				t.Fatalf("expected one upstream request, got %d (status %d)", len(upstream), rec.Code)
			}
			if got := upstream[0].URL.EscapedPath(); got != tc.want {
				t.Fatalf("upstream escaped path %q, want %q", got, tc.want)
			}
		})
	}
}

// A path carrying dot segments is refused rather than forwarded: after
// stripping, "/emby/%2e%2e%2f%2e%2e/etc/passwd" would otherwise reach the
// companion as "/../../etc/passwd".
func TestDotSegmentPathsAreRefused(t *testing.T) {
	for _, path := range []string{
		"/emby/%2e%2e%2f%2e%2e/etc/passwd",
		"/emby/../../etc/passwd",
		"/audiobookshelf/api/%2e%2e/secrets",
		"/Items/..%2f..%2fetc/passwd",
		"/jellyfin/Items/./x",
	} {
		transport := &recordingTransport{}
		states := &fakeStates{}
		states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
		states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
		gateway := newTestGateway(t, states, transport)

		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q answered %d, want 400", path, rec.Code)
		}
		if len(transport.recorded()) != 0 {
			t.Fatalf("path %q was forwarded to a companion", path)
		}
	}
}

// An encoded slash inside the family segment makes the decoded and escaped
// forms strip to different paths; net/url then discards RawPath and the
// encoding guarantee is silently lost. Refuse rather than forward.
func TestInconsistentlyEncodedPrefixIsRefused(t *testing.T) {
	for _, path := range []string{
		"/emby%2fItems/x",
		"/emby%2FItems/x",
		"/audiobookshelf%2fapi/ping",
	} {
		transport := &recordingTransport{}
		states := &fakeStates{}
		states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
		states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
		gateway := newTestGateway(t, states, transport)

		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q answered %d, want 400", path, rec.Code)
		}
		if len(transport.recorded()) != 0 {
			t.Fatalf("path %q was forwarded with an inconsistent prefix", path)
		}
	}
}

// Hardening beyond what any current companion decodes: a doubly-encoded dot
// segment and a dot segment hidden behind a path parameter are refused too,
// so a companion that normalizes either cannot be walked out of its tree.
func TestObfuscatedDotSegmentsAreRefused(t *testing.T) {
	for _, path := range []string{
		"/audiobookshelf/%252e%252e/x",
		"/Items/..;/admin",
		"/emby/%2e%2e;foo/x",
	} {
		transport := &recordingTransport{}
		states := &fakeStates{}
		states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
		states.set(KindAudiobookshelf, availableStatus(mustParseURL(t, "http://bloem-audiobookshelf:13378")))
		gateway := newTestGateway(t, states, transport)

		rec := httptest.NewRecorder()
		gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q answered %d, want 400", path, rec.Code)
		}
		if len(transport.recorded()) != 0 {
			t.Fatalf("path %q was forwarded", path)
		}
	}
}

// --- Local (first-party, in-process) dispatch -------------------------------

// recordingHandler is a stand-in for the in-process jellycompat/ABS handlers:
// it records the request it received and answers a fixed body, so a test can
// assert the gateway actually reached it (not the States-based proxy path)
// and can inspect exactly what path/prefix it saw.
type recordingHandler struct {
	mu       sync.Mutex
	requests []*http.Request
	body     string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.requests = append(h.requests, r)
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.body))
}

func (h *recordingHandler) received() []*http.Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*http.Request, len(h.requests))
	copy(out, h.requests)
	return out
}

// A route family with a registered local handler is answered by that
// handler directly — not proxied — even when States has no record of the
// application at all (no enrollment, no endpoint). This is the behavior the
// in-process Jellyfin/ABS wiring in cmd/silo/main.go depends on: those two
// services have no Endpoint and no enrollment record, ever.
func TestLocalHandlerServesWithoutStates(t *testing.T) {
	transport := &recordingTransport{}
	jf := &recordingHandler{body: "jellyfin-local"}
	gateway := New(Config{
		States:         &fakeStates{}, // no record of KindJellyfin at all
		Transport:      transport,
		IdentitySecret: []byte("gateway-test-secret"),
		LocalHandlers:  map[AppKind]http.Handler{KindJellyfin: jf},
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 (local handler answer)", rec.Code)
	}
	if rec.Body.String() != "jellyfin-local" {
		t.Fatalf("got body %q, want the local handler's own response", rec.Body.String())
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("a locally-dispatched request must never reach the upstream transport")
	}
	if got := jf.received(); len(got) != 1 || got[0].URL.Path != "/System/Info" {
		t.Fatalf("local handler saw %v, want exactly one request for /System/Info", got)
	}
}

// The legacy /emby and /jellyfin base paths are still handed to the local
// Jellyfin handler with their prefix stripped, exactly as the proxy path
// strips it before forwarding to a real companion endpoint.
func TestLocalHandlerStripsPrefixForLegacyJellyfinBasePaths(t *testing.T) {
	jf := &recordingHandler{body: "ok"}
	gateway := New(Config{
		States:         &fakeStates{},
		IdentitySecret: []byte("gateway-test-secret"),
		LocalHandlers:  map[AppKind]http.Handler{KindJellyfin: jf},
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/emby/Items/42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	got := jf.received()
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	if got[0].URL.Path != "/Items/42" {
		t.Fatalf("local handler saw path %q, want /emby stripped to /Items/42", got[0].URL.Path)
	}
}

// The Audiobookshelf route family strips its public prefix before reaching
// the local handler, matching what the dedicated ABS listener has always
// received (protocol-native paths, no /audiobookshelf prefix).
func TestLocalHandlerStripsPrefixForAudiobookshelf(t *testing.T) {
	abs := &recordingHandler{body: "ok"}
	gateway := New(Config{
		States:         &fakeStates{},
		IdentitySecret: []byte("gateway-test-secret"),
		LocalHandlers:  map[AppKind]http.Handler{KindAudiobookshelf: abs},
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/api/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	got := abs.received()
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	if got[0].URL.Path != "/api/ping" {
		t.Fatalf("local handler saw path %q, want /audiobookshelf stripped to /api/ping", got[0].URL.Path)
	}
}

// This is the default-deny pin: registering a local handler for one
// application must not open up routing for the other, unrelated one. A
// route family whose application has no local handler AND no routable
// States entry keeps answering compatibility_unavailable exactly as an
// unenrolled companion always has — a local handler is opt-in per
// application, not a gateway-wide bypass.
func TestUnregisteredLocalHandlerStillFailsClosed(t *testing.T) {
	transport := &recordingTransport{}
	jf := &recordingHandler{body: "jellyfin-local"}
	// Jellyfin has a local handler and no States record; Audiobookshelf has
	// neither a local handler nor a States record.
	gateway := New(Config{
		States:         &fakeStates{},
		Transport:      transport,
		IdentitySecret: []byte("gateway-test-secret"),
		LocalHandlers:  map[AppKind]http.Handler{KindJellyfin: jf},
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audiobookshelf/api/ping", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503 compatibility_unavailable", rec.Code)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "compatibility_unavailable" {
		t.Fatalf("body %q must carry compatibility_unavailable", rec.Body.String())
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("an unregistered, unrouteable application must not be dialed")
	}
	if len(jf.received()) != 0 {
		t.Fatal("a request for a different application family must never reach an unrelated local handler")
	}

	// Jellyfin's own family, meanwhile, still resolves locally.
	rec2 := httptest.NewRecorder()
	gateway.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("jellyfin local dispatch got status %d, want 200", rec2.Code)
	}
}

// A local handler takes priority over an enrolled companion's States entry:
// first-party in-process services are never proxied even if an operator
// somehow also has a routable enrollment record for the same AppKind.
func TestLocalHandlerTakesPriorityOverRoutableStates(t *testing.T) {
	transport := &recordingTransport{}
	states := &fakeStates{}
	states.set(KindJellyfin, availableStatus(mustParseURL(t, "http://bloem-jellyfin:8096")))
	jf := &recordingHandler{body: "jellyfin-local"}
	gateway := New(Config{
		States:         states,
		Transport:      transport,
		IdentitySecret: []byte("gateway-test-secret"),
		LocalHandlers:  map[AppKind]http.Handler{KindJellyfin: jf},
	})

	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/System/Info", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "jellyfin-local" {
		t.Fatalf("got status %d body %q, want the local handler's own 200 response", rec.Code, rec.Body.String())
	}
	if len(transport.recorded()) != 0 {
		t.Fatal("a request must not be proxied when a local handler is registered for the same AppKind")
	}
}
