package lanadvert

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/brutella/dnssd"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
	"github.com/Silo-Server/silo-server/internal/serverid"
)

// fakeSettings is a serverid.Store over a plain map, mirroring the repo
// behavior of "missing row reads as empty with no error".
type fakeSettings map[string]string

func (s fakeSettings) Get(_ context.Context, key string) (string, error) {
	return s[key], nil
}

func (s fakeSettings) Set(_ context.Context, key, value string) error {
	s[key] = value
	return nil
}

// fakeHandle is a dnssd.ServiceHandle bound to the service it registered.
type fakeHandle struct{ svc dnssd.Service }

func (h *fakeHandle) UpdateText(map[string]string, dnssd.Responder) {}
func (h *fakeHandle) Service() dnssd.Service                        { return h.svc }

// fakeResponder records Add/Remove and holds Respond open until its context
// is canceled, like the real responder's query loop.
type fakeResponder struct {
	mu      sync.Mutex
	added   []dnssd.Service
	handles []*fakeHandle
	removed []dnssd.ServiceHandle

	respondErr error
}

func (f *fakeResponder) Add(svc dnssd.Service) (dnssd.ServiceHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	handle := &fakeHandle{svc: svc}
	f.added = append(f.added, svc)
	f.handles = append(f.handles, handle)
	return handle, nil
}

func (f *fakeResponder) Remove(h dnssd.ServiceHandle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, h)
}

func (f *fakeResponder) Respond(ctx context.Context) error {
	<-ctx.Done()
	if f.respondErr != nil {
		return f.respondErr
	}
	return ctx.Err()
}

func (f *fakeResponder) Debug(context.Context, dnssd.ReadFunc) {}

func (f *fakeResponder) addCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.added)
}

func (f *fakeResponder) removeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.removed)
}

// capturingHandler collects every record the advertiser emits, so tests can
// assert the degradation paths stay quiet — at most one line, and at the
// right level.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

func staticName(context.Context) string { return "Test Server" }

// TestTXTRecordMatchesIdentityContract pins the TXT contract to the identity
// document's own variables. The api and bloem expectations are derived from
// the handlers accessors — never literals — so the record cannot drift from
// the identity document; the second assertion per key pins the values the
// clients shipped parsing, so any change to those variables fails here and
// forces a conscious review of both sides.
func TestTXTRecordMatchesIdentityContract(t *testing.T) {
	id := Identity{
		ServerID:   "0d5b3f7a-1111-4222-8333-444455556666",
		ServerName: "Living Room",
		Scheme:     SchemeHTTPS,
		Port:       8443,
	}
	txt := txtRecord(id)

	wantKeys := []string{"txtvers", "id", "name", "scheme", "api", "bloem"}
	if len(txt) != len(wantKeys) {
		t.Fatalf("TXT record has %d keys (%v), want exactly %v", len(txt), txt, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := txt[key]; !ok {
			t.Fatalf("TXT record is missing key %q: %v", key, txt)
		}
	}

	if txt["txtvers"] != "1" {
		t.Errorf("txtvers = %q, want 1", txt["txtvers"])
	}
	if txt["id"] != id.ServerID {
		t.Errorf("id = %q, want the resolved server instance id %q", txt["id"], id.ServerID)
	}
	if txt["name"] != id.ServerName {
		t.Errorf("name = %q, want the operator-facing server name %q", txt["name"], id.ServerName)
	}
	if txt["scheme"] != id.Scheme {
		t.Errorf("scheme = %q, want the served scheme %q", txt["scheme"], id.Scheme)
	}

	wantAPI := ""
	for i, major := range handlers.ServerAPIMajorVersions() {
		if i > 0 {
			wantAPI += ","
		}
		wantAPI += strconv.Itoa(major)
	}
	if len(handlers.ServerAPIMajorVersions()) != 1 || handlers.ServerAPIMajorVersions()[0] != 1 {
		t.Errorf("serverAPIMajorVersions = %v; the pinned expectation below only holds for the current contract and must be updated consciously", handlers.ServerAPIMajorVersions())
	}
	if txt["api"] != wantAPI {
		t.Errorf("api = %q, want %q: the record must carry exactly the Silo-compatible majors the identity document publishes", txt["api"], wantAPI)
	}
	if wantAPI != "1" {
		t.Errorf("api majors %v produce %q; clients shipped parsing \"1\" — update this pin together with serverAPIMajorVersions", handlers.ServerAPIMajorVersions(), wantAPI)
	}

	wantBloem := ""
	for i, surface := range handlers.NativeAPISurfaces() {
		if i > 0 {
			wantBloem += ","
		}
		wantBloem += surface
	}
	if txt["bloem"] != wantBloem {
		t.Errorf("bloem = %q, want %q: the record must carry exactly the native surfaces the identity document publishes", txt["bloem"], wantBloem)
	}
	if len(handlers.NativeAPISurfaces()) != 1 || handlers.NativeAPISurfaces()[0] != "v1" {
		t.Errorf("nativeAPISurfaces = %v; the pinned expectation below only holds for the current contract and must be updated consciously", handlers.NativeAPISurfaces())
	}
	if wantBloem != "v1" {
		t.Errorf("native surfaces %v produce %q; clients shipped parsing \"v1\" — update this pin together with nativeAPISurfaces", handlers.NativeAPISurfaces(), wantBloem)
	}
}

// TestStartAdvertisesResolvedIdentity drives the happy path end to end: the
// identity is resolved from a settings store, the service is registered with
// the exact TXT record, and Stop sends the goodbye and stays idempotent.
func TestStartAdvertisesResolvedIdentity(t *testing.T) {
	settings := fakeSettings{}
	resolver := serverid.NewResolver(settings)
	serverID, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve server id: %v", err)
	}

	responder := &fakeResponder{}
	logger := &capturingHandler{}
	cfg := Config{
		Listen:       ":8090",
		Scheme:       SchemeHTTP,
		Identity:     resolver,
		ServerName:   staticName,
		Interfaces:   func() []*net.Interface { return []*net.Interface{{Name: "eth0"}} },
		NewResponder: func() (dnssd.Responder, error) { return responder, nil },
		Logger:       slog.New(logger),
	}

	adv := Start(context.Background(), cfg)
	if got := adv.State(); got != StateActive {
		t.Fatalf("state after Start = %v, want active", got)
	}
	if responder.addCount() != 1 {
		t.Fatalf("service registrations = %d, want 1", responder.addCount())
	}

	svc := responder.handles[0].svc
	if svc.Name != "Test Server" {
		t.Errorf("instance name = %q, want the server name", svc.Name)
	}
	if svc.Type != ServiceType {
		t.Errorf("service type = %q, want %q", svc.Type, ServiceType)
	}
	if svc.Port != 8090 {
		t.Errorf("port = %d, want 8090 from the listen address", svc.Port)
	}

	wantTXT := txtRecord(Identity{ServerID: serverID, ServerName: "Test Server", Scheme: SchemeHTTP, Port: 8090})
	for key, want := range wantTXT {
		if got := svc.Text[key]; got != want {
			t.Errorf("TXT[%s] = %q, want %q", key, got, want)
		}
	}
	if len(svc.Text) != len(wantTXT) {
		t.Errorf("TXT record has %d keys (%v), want %d", len(svc.Text), svc.Text, len(wantTXT))
	}

	adv.Stop()
	if got := adv.State(); got != StateStopped {
		t.Fatalf("state after Stop = %v, want stopped", got)
	}
	if responder.removeCount() != 1 {
		t.Fatalf("deregistrations = %d, want exactly one goodbye", responder.removeCount())
	}

	// A second Stop must not deregister twice.
	adv.Stop()
	if responder.removeCount() != 1 {
		t.Fatalf("deregistrations after second Stop = %d, want still 1", responder.removeCount())
	}
}

// TestStartWithoutMulticastStaysQuiet drives the bridge-networked/LXC shape:
// no multicast-capable interface exists, so the advertiser must give up once,
// quietly, and never attempt to register.
func TestStartWithoutMulticastStaysQuiet(t *testing.T) {
	resolver := serverid.NewResolver(fakeSettings{})
	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve server id: %v", err)
	}

	responderBuilt := false
	logger := &capturingHandler{}
	cfg := Config{
		Listen:     ":8080",
		Identity:   resolver,
		ServerName: staticName,
		Interfaces: func() []*net.Interface { return nil },
		NewResponder: func() (dnssd.Responder, error) {
			responderBuilt = true
			return &fakeResponder{}, nil
		},
		Logger: slog.New(logger),
	}

	adv := Start(context.Background(), cfg)
	if got := adv.State(); got != StateInactive {
		t.Fatalf("state = %v, want inactive", got)
	}
	if responderBuilt {
		t.Fatal("a responder was built on a host with no multicast interface; the unavailable path must not attempt registration")
	}
	adv.Stop()
	if got := adv.State(); got != StateInactive {
		t.Fatalf("state after Stop = %v, want still inactive", got)
	}

	records := logger.all()
	if len(records) != 1 {
		t.Fatalf("log records = %d (%v), want exactly one quiet degradation line and nothing more", len(records), records)
	}
	if records[0].Level != slog.LevelDebug {
		t.Errorf("degradation level = %v, want debug: a host without multicast is a normal shape, not an incident", records[0].Level)
	}
}

// TestStartWithoutIdentityDoesNotAdvertise pins the rule that an
// unresolvable identity means no advertisement at all — never an invented
// identifier, which would silently re-key every client's stored state.
func TestStartWithoutIdentityDoesNotAdvertise(t *testing.T) {
	responderBuilt := false
	logger := &capturingHandler{}
	cfg := Config{
		Listen:     ":8080",
		Identity:   serverid.NewResolver(nil), // no settings store: Resolve returns ErrUnavailable
		ServerName: staticName,
		Interfaces: func() []*net.Interface { return []*net.Interface{{Name: "eth0"}} },
		NewResponder: func() (dnssd.Responder, error) {
			responderBuilt = true
			return &fakeResponder{}, nil
		},
		Logger: slog.New(logger),
	}

	adv := Start(context.Background(), cfg)
	if got := adv.State(); got != StateInactive {
		t.Fatalf("state = %v, want inactive", got)
	}
	if responderBuilt {
		t.Fatal("a responder was built without a resolvable identity")
	}

	records := logger.all()
	if len(records) != 1 {
		t.Fatalf("log records = %d (%v), want exactly one", len(records), records)
	}
	if records[0].Level != slog.LevelDebug {
		t.Errorf("degradation level = %v, want debug", records[0].Level)
	}
}

// TestStartWhenResponderUnavailableStaysQuiet covers the remaining
// degradation shape: the responder cannot be opened (e.g. mDNS blocked). One
// line, no retry, no registration.
func TestStartWhenResponderUnavailableStaysQuiet(t *testing.T) {
	resolver := serverid.NewResolver(fakeSettings{})
	if _, err := resolver.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve server id: %v", err)
	}

	attempts := 0
	logger := &capturingHandler{}
	cfg := Config{
		Listen:     ":8080",
		Identity:   resolver,
		ServerName: staticName,
		Interfaces: func() []*net.Interface { return []*net.Interface{{Name: "eth0"}} },
		NewResponder: func() (dnssd.Responder, error) {
			attempts++
			return nil, context.DeadlineExceeded
		},
		Logger: slog.New(logger),
	}

	adv := Start(context.Background(), cfg)
	if got := adv.State(); got != StateInactive {
		t.Fatalf("state = %v, want inactive", got)
	}
	adv.Stop()

	if attempts != 1 {
		t.Fatalf("responder attempts = %d, want exactly one: the advertiser must not retry on a timer", attempts)
	}
	records := logger.all()
	if len(records) != 1 {
		t.Fatalf("log records = %d (%v), want exactly one", len(records), records)
	}
	if records[0].Level != slog.LevelWarn {
		t.Errorf("degradation level = %v, want warn: a blocked responder is unexpected, and worth the single line", records[0].Level)
	}
}

func TestPortFromListen(t *testing.T) {
	cases := []struct {
		listen string
		port   int
		ok     bool
	}{
		{":8080", 8080, true},
		{"127.0.0.1:8080", 8080, true},
		{"0.0.0.0:80", 80, true},
		{"8080", 0, false},   // no host:port pair
		{":http", 0, false},  // named port, not numeric
		{":0", 0, false},     // port 0 is not servable
		{":99999", 0, false}, // out of range
		{"", 0, false},
	}
	for _, tc := range cases {
		port, ok := portFromListen(tc.listen)
		if ok != tc.ok || port != tc.port {
			t.Errorf("portFromListen(%q) = (%d, %v), want (%d, %v)", tc.listen, port, ok, tc.port, tc.ok)
		}
	}
}
