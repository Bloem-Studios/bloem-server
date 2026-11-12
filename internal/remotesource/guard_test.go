package remotesource

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlockedAddr(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.255.0.9", "::1", "0.0.0.0", "0.1.2.3", "::",
		"10.0.0.5", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "fe80::1", "fc00::1", "fd00:ec2::254",
		"100.64.0.1", "100.100.100.200", "100.127.255.255",
		"224.0.0.1", "239.255.255.250", "ff02::1", "255.255.255.255", "240.0.0.1",
		"192.0.0.170", "198.18.0.1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::a9fe:a9fe", // NAT64 of 169.254.169.254
		"2002:7f00:1::",      // 6to4 of 127.0.0.1
		"fec0::1",
	}
	for _, raw := range blocked {
		if !BlockedAddr(netip.MustParseAddr(raw)) {
			t.Errorf("BlockedAddr(%s) = false, want true", raw)
		}
	}
	public := []string{"93.184.216.34", "1.1.1.1", "8.8.8.8", "2606:4700:4700::1111", "100.63.255.255", "100.128.0.1", "172.32.0.1", "64:ff9b::808:808", "2002:808:808::"}
	for _, raw := range public {
		if BlockedAddr(netip.MustParseAddr(raw)) {
			t.Errorf("BlockedAddr(%s) = true, want false", raw)
		}
	}
	if !BlockedAddr(netip.Addr{}) {
		t.Error("the zero address must be blocked")
	}
}

func TestBlockedName(t *testing.T) {
	for _, name := range []string{"localhost", "LOCALHOST.", "db.localhost", "metadata", "metadata.google.internal", "instance-data"} {
		if !BlockedName(name) {
			t.Errorf("BlockedName(%q) = false", name)
		}
	}
	if BlockedName("media.example.net") {
		t.Error("an ordinary name is not blocked")
	}
}

// fakeResolver answers each host from a queue; the last answer repeats.
type fakeResolver struct {
	answers map[string][][]netip.Addr
	calls   atomic.Int32
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls.Add(1)
	queue := r.answers[host]
	if len(queue) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	answer := queue[0]
	if len(queue) > 1 {
		r.answers[host] = queue[1:]
	}
	return answer, nil
}

func addrs(raw ...string) []netip.Addr {
	out := make([]netip.Addr, len(raw))
	for i, s := range raw {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

// testGuard treats 127.0.0.1 as a public address, so loopback test servers
// stand in for the internet, while 127.0.0.2 and every other private address
// keep the real policy.
func testGuard(resolver Resolver, allow *atomic.Bool) *Guard {
	opts := Options{Resolver: resolver}
	if allow != nil {
		opts.AllowPrivate = func(context.Context) bool { return allow.Load() }
	}
	g := NewGuard(opts)
	g.public = func(a netip.Addr) bool { return a == netip.MustParseAddr("127.0.0.1") }
	return g
}

func serverOn(t *testing.T, ip string, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", ip, err)
	}
	server := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}}
	server.Start()
	t.Cleanup(server.Close)
	return server
}

func port(t *testing.T, server *httptest.Server) string {
	t.Helper()
	_, p, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func get(ctx context.Context, g *Guard, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := g.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestGuardRefusesLiteralPrivateAddressesWithoutConnecting(t *testing.T) {
	g := NewGuard(Options{Resolver: &fakeResolver{}})
	for _, raw := range []string{
		"http://127.0.0.1:1/x", "http://[::1]:1/x", "http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/", "http://[::ffff:192.168.0.1]/", "http://100.100.100.200/", "http://localhost/",
		"http://metadata.google.internal/computeMetadata/v1/", "http://[fe80::1%25eth0]/",
	} {
		if err := g.Check(context.Background(), raw); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("Check(%s) = %v, want ErrBlockedAddress", raw, err)
		}
		if _, err := get(context.Background(), g, raw); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("GET %s = %v, want ErrBlockedAddress", raw, err)
		}
	}
	for _, raw := range []string{"ftp://example.net/x", "file:///etc/passwd", "gopher://x/", "//example.net/x", "/movies/x.mkv", "http:///x"} {
		if err := g.Check(context.Background(), raw); !errors.Is(err, ErrInvalidStrm) {
			t.Errorf("Check(%s) = %v, want ErrInvalidStrm", raw, err)
		}
	}
}

func TestGuardAllowsPublicAndHonoursTheSetting(t *testing.T) {
	private := serverOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "private") }))
	public := serverOn(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "public") }))
	resolver := &fakeResolver{answers: map[string][][]netip.Addr{
		"media.test":    {addrs("127.0.0.1")},
		"internal.test": {addrs("127.0.0.2")},
	}}
	var allow atomic.Bool
	g := testGuard(resolver, &allow)
	ctx := context.Background()

	if body, err := get(ctx, g, "http://media.test:"+port(t, public)+"/"); err != nil || body != "public" {
		t.Fatalf("public GET = %q, %v", body, err)
	}
	if _, err := get(ctx, g, "http://internal.test:"+port(t, private)+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("private GET = %v, want ErrBlockedAddress", err)
	}
	allow.Store(true)
	if body, err := get(ctx, g, "http://internal.test:"+port(t, private)+"/"); err != nil || body != "private" {
		t.Fatalf("private GET with the setting on = %q, %v", body, err)
	}
	allow.Store(false)
	if _, err := get(ctx, g, "http://127.0.0.2:"+port(t, private)+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("private GET after the setting went off = %v, want ErrBlockedAddress", err)
	}
}

// DNS rebinding: the name answers a public address for the early check and a
// private one when the connection is made. The dial resolves again and
// refuses.
func TestGuardRefusesDNSRebindingAtConnect(t *testing.T) {
	private := serverOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "private") }))
	resolver := &fakeResolver{answers: map[string][][]netip.Addr{"rebind.test": {addrs("127.0.0.1"), addrs("127.0.0.2")}}}
	g := testGuard(resolver, nil)
	target := "http://rebind.test:" + port(t, private) + "/"
	if err := g.Check(context.Background(), target); err != nil {
		t.Fatalf("the first answer is public: Check = %v", err)
	}
	if _, err := get(context.Background(), g, target); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("GET after rebinding = %v, want ErrBlockedAddress", err)
	}
	if resolver.calls.Load() < 2 {
		t.Fatalf("the dial must resolve again, resolver calls = %d", resolver.calls.Load())
	}
}

// A name that answers both a public and a private address connects only to
// the public one.
func TestGuardSkipsPrivateAnswersOfAMixedName(t *testing.T) {
	var privateHits atomic.Int32
	private := serverOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { privateHits.Add(1) }))
	resolver := &fakeResolver{answers: map[string][][]netip.Addr{"mixed.test": {addrs("127.0.0.2")}}}
	g := testGuard(resolver, nil)
	if _, err := get(context.Background(), g, "http://mixed.test:"+port(t, private)+"/"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("GET = %v, want ErrBlockedAddress", err)
	}
	if privateHits.Load() != 0 {
		t.Fatal("the private server was reached")
	}
}

func TestGuardRefusesRedirectsToPrivateAddresses(t *testing.T) {
	var privateHits atomic.Int32
	private := serverOn(t, "127.0.0.2", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		privateHits.Add(1)
		_, _ = io.WriteString(w, "secret")
	}))
	privateURL := "http://127.0.0.2:" + port(t, private) + "/latest/meta-data/"
	resolver := &fakeResolver{answers: map[string][][]netip.Addr{"inside.test": {addrs("127.0.0.2")}}}
	public := serverOn(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/literal":
			http.Redirect(w, r, privateURL, http.StatusFound)
		case "/name":
			http.Redirect(w, r, "http://inside.test:"+port(t, private)+"/", http.StatusTemporaryRedirect)
		case "/scheme":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/hop":
			http.Redirect(w, r, "/literal", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "fine")
		}
	}))
	g := testGuard(resolver, nil)
	base := "http://127.0.0.1:" + port(t, public)
	for _, path := range []string{"/literal", "/name", "/hop"} {
		if _, err := get(context.Background(), g, base+path); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("redirect %s = %v, want ErrBlockedAddress", path, err)
		}
	}
	if _, err := get(context.Background(), g, base+"/scheme"); err == nil {
		t.Error("a redirect to file:// must fail")
	}
	if privateHits.Load() != 0 {
		t.Fatalf("a redirect reached the private server %d times", privateHits.Load())
	}
	if body, err := get(context.Background(), g, base+"/ok"); err != nil || body != "fine" {
		t.Fatalf("plain GET = %q, %v", body, err)
	}
}

func TestGuardBoundsRedirectChains(t *testing.T) {
	var hits atomic.Int32
	loop := serverOn(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	g := testGuard(&fakeResolver{}, nil)
	_, err := get(context.Background(), g, "http://127.0.0.1:"+port(t, loop)+"/")
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("redirect loop = %v", err)
	}
	if hits.Load() > MaxRedirects+1 {
		t.Fatalf("followed %d hops", hits.Load())
	}
}

func TestGuardIgnoresEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.2:9")
	t.Setenv("http_proxy", "http://127.0.0.2:9")
	public := serverOn(t, "127.0.0.1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "direct") }))
	g := testGuard(&fakeResolver{}, nil)
	if body, err := get(context.Background(), g, "http://127.0.0.1:"+port(t, public)+"/"); err != nil || body != "direct" {
		t.Fatalf("GET = %q, %v", body, err)
	}
}
