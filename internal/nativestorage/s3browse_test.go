package nativestorage

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// Credentials used by every fake request. Tests assert none of these bytes
// appear in any error text or captured log line.
const (
	testS3AccessKey    = "AKIATESTBROWSEKEY0001"
	testS3SecretKey    = "s3cr3t/BROWSE+secret+value+0001"
	testS3SessionToken = "SESSIONTOKEN-browse-0001"
	testPublicIPv4     = "93.184.216.34"
)

func testS3Connection(endpoint string) S3BrowseConnection {
	return S3BrowseConnection{Endpoint: endpoint, Region: "us-east-1", AccessKey: testS3AccessKey, SecretKey: testS3SecretKey, SessionToken: testS3SessionToken}
}

func assertNoS3Credentials(t *testing.T, label, text string) {
	t.Helper()
	for _, secret := range []string{testS3AccessKey, testS3SecretKey, testS3SessionToken, "BROWSE+secret", "browse-0001"} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked credential bytes", label)
		}
	}
}

type fakeS3Resolver struct {
	mu      sync.Mutex
	answers map[string][][]netip.Addr // successive answers per host; the last repeats
	calls   map[string]int
	err     error
}

func (r *fakeS3Resolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = map[string]int{}
	}
	r.calls[host]++
	if r.err != nil {
		return nil, r.err
	}
	answers := r.answers[host]
	if len(answers) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	i := r.calls[host] - 1
	if i >= len(answers) {
		i = len(answers) - 1
	}
	return answers[i], nil
}

func addrs(values ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(values))
	for _, v := range values {
		out = append(out, netip.MustParseAddr(v))
	}
	return out
}

// fakeS3 is an httptest TLS server whose certificate covers example.com. The
// test-only dial seam connects the pinned public address to it, so the
// production resolve/check/pin path runs unchanged.
type fakeS3 struct {
	server   *httptest.Server
	browser  *S3Browser
	resolver *fakeS3Resolver
	dialed   []string
	dialMu   sync.Mutex
}

func newFakeS3(t *testing.T, handler http.HandlerFunc) *fakeS3 {
	t.Helper()
	f := &fakeS3{resolver: &fakeS3Resolver{answers: map[string][][]netip.Addr{"example.com": {addrs(testPublicIPv4)}}}}
	f.server = httptest.NewTLSServer(handler)
	t.Cleanup(f.server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(f.server.Certificate())
	f.browser = NewS3Browser()
	f.browser.resolver = f.resolver
	f.browser.rootCAs = pool
	f.browser.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		f.dialMu.Lock()
		f.dialed = append(f.dialed, address)
		f.dialMu.Unlock()
		if address != net.JoinHostPort(testPublicIPv4, "443") {
			return nil, fmt.Errorf("test dial seam: unexpected address")
		}
		var d net.Dialer
		return d.DialContext(ctx, network, f.server.Listener.Addr().String())
	}
	return f
}

func (f *fakeS3) dialedAddresses() []string {
	f.dialMu.Lock()
	defer f.dialMu.Unlock()
	return append([]string(nil), f.dialed...)
}

func nativeCode(err error) string {
	var typed *catalog.NativeOnboardingError
	if errors.As(err, &typed) && typed != nil {
		return typed.Code
	}
	return ""
}

func TestParseS3BrowseEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		raw      string
		restrict bool
		ok       bool
		base     string
	}{
		{"https_public", "https://s3.example.com", false, true, "https://s3.example.com:443"},
		{"https_root_path", "https://s3.example.com/", false, true, "https://s3.example.com:443"},
		{"https_port", "https://s3.example.com:9000", false, true, "https://s3.example.com:9000"},
		{"uppercase_host", "https://S3.Example.COM", false, true, "https://s3.example.com:443"},
		{"public_ipv4_literal", "https://93.184.216.34", false, true, "https://93.184.216.34:443"},
		{"public_ipv6_literal", "https://[2606:2800:220:1:248:1893:25c8:1946]:8443", false, true, "https://[2606:2800:220:1:248:1893:25c8:1946]:8443"},
		{"empty", "", false, false, ""},
		{"http", "http://s3.example.com", false, false, ""},
		{"no_scheme", "s3.example.com", false, false, ""},
		{"ftp", "ftp://s3.example.com", false, false, ""},
		{"userinfo", "https://user:pass@s3.example.com", false, false, ""},
		{"user_only", "https://user@s3.example.com", false, false, ""},
		{"path", "https://s3.example.com/bucket", false, false, ""},
		{"query", "https://s3.example.com?x=1", false, false, ""},
		{"force_query", "https://s3.example.com?", false, false, ""},
		{"fragment", "https://s3.example.com#frag", false, false, ""},
		{"port_zero", "https://s3.example.com:0", false, false, ""},
		{"port_70000", "https://s3.example.com:70000", false, false, ""},
		{"empty_port", "https://s3.example.com:", false, false, ""},
		{"too_long", "https://" + strings.Repeat("a", 2050) + ".com", false, false, ""},
		{"control", "https://s3.example.com\n", false, false, ""},
		{"backslash", "https://s3.example.com\\@evil", false, false, ""},
		{"zone", "https://[fe80::1%25eth0]", false, false, ""},
		{"org_443", "https://s3.example.com", true, true, "https://s3.example.com:443"},
		{"org_7480", "https://s3.example.com:7480", true, true, "https://s3.example.com:7480"},
		{"org_8443", "https://s3.example.com:8443", true, true, "https://s3.example.com:8443"},
		{"org_9000", "https://s3.example.com:9000", true, true, "https://s3.example.com:9000"},
		{"org_9443", "https://s3.example.com:9443", true, true, "https://s3.example.com:9443"},
		{"org_22", "https://s3.example.com:22", true, false, ""},
		{"org_6379", "https://s3.example.com:6379", true, false, ""},
		{"platform_6379", "https://s3.example.com:6379", false, true, "https://s3.example.com:6379"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseS3BrowseEndpoint(tc.raw, tc.restrict)
			if tc.ok {
				if err != nil || got.Base != tc.base {
					t.Fatalf("ParseS3BrowseEndpoint = %+v, %v; want base %q", got, err, tc.base)
				}
				return
			}
			if code := nativeCode(err); code != S3EndpointRejectedCode {
				t.Fatalf("code = %q, want %q", code, S3EndpointRejectedCode)
			}
		})
	}
}

func TestS3BrowseBlockedIPLiterals(t *testing.T) {
	for _, ip := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "10.255.255.255", "100.64.0.1", "100.127.255.254", "127.0.0.1", "127.8.8.8",
		"169.254.169.254", "169.254.0.1", "172.16.0.1", "172.31.255.255", "192.0.0.8", "192.168.1.1",
		"198.18.0.1", "198.19.255.255", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
		"::1", "::", "fc00::1", "fd00:ec2::254", "fe80::1", "febf::1", "ff02::1", "ff05::2",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254", "::ffff:192.168.0.1", "::ffff:100.64.0.1",
		"::127.0.0.1", "64:ff9b::a00:1", "2002:a00:1::1",
	} {
		t.Run(ip, func(t *testing.T) {
			if !S3BrowseAddressBlocked(netip.MustParseAddr(ip)) {
				t.Fatalf("%s not blocked", ip)
			}
			host := ip
			if strings.Contains(ip, ":") {
				host = "[" + ip + "]"
			}
			if _, err := ParseS3BrowseEndpoint("https://"+host, false); nativeCode(err) != S3EndpointRejectedCode {
				t.Fatalf("literal endpoint %s accepted: %v", ip, err)
			}
		})
	}
	for _, ip := range []string{"93.184.216.34", "8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "172.32.0.1", "198.20.0.1", "2606:4700::1111", "::ffff:93.184.216.34"} {
		if S3BrowseAddressBlocked(netip.MustParseAddr(ip)) {
			t.Fatalf("public %s blocked", ip)
		}
	}
}

func TestS3BrowseResolvedHostBlocked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  []netip.Addr
		dnsErr  error
		allowed bool
	}{
		{"public", addrs(testPublicIPv4), nil, true},
		{"public_v6", addrs("2606:4700::1111"), nil, true},
		{"loopback", addrs("127.0.0.1"), nil, false},
		{"private", addrs("10.1.2.3"), nil, false},
		{"metadata", addrs("169.254.169.254"), nil, false},
		{"cgnat", addrs("100.100.100.200"), nil, false},
		{"unique_local", addrs("fd12:3456::1"), nil, false},
		{"one_of_many_blocked", addrs(testPublicIPv4, "8.8.8.8", "192.168.0.10"), nil, false},
		{"one_of_many_blocked_v6", addrs("2606:4700::1111", "::1"), nil, false},
		{"mapped_private", addrs("::ffff:172.16.5.4"), nil, false},
		{"empty_answer", nil, nil, false},
		{"dns_failure", nil, &net.DNSError{Err: "server misbehaving", Name: "example.com"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &fakeS3Resolver{answers: map[string][][]netip.Addr{"example.com": {tc.answer}}, err: tc.dnsErr}
			browser := NewS3Browser()
			browser.resolver = resolver
			endpoint, err := ParseS3BrowseEndpoint("https://example.com", false)
			if err != nil {
				t.Fatal(err)
			}
			target, err := browser.resolveS3BrowseTarget(t.Context(), endpoint)
			if tc.allowed {
				if err != nil || len(target.addrs) == 0 {
					t.Fatalf("public host refused: %v", err)
				}
				return
			}
			if nativeCode(err) != S3EndpointRejectedCode {
				t.Fatalf("code = %q, want %q", nativeCode(err), S3EndpointRejectedCode)
			}
		})
	}
}

// DNS rebinding: the resolver answers a public address for the check and a
// private one afterwards. The dial is pinned to the checked address and never
// resolves again, so the private address is never contacted.
func TestS3BrowseDialPinnedAgainstRebinding(t *testing.T) {
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listBucketsXML("media")))
	})
	f.resolver.answers["example.com"] = [][]netip.Addr{addrs(testPublicIPv4), addrs("10.0.0.5"), addrs("127.0.0.1")}
	buckets, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"))
	if err != nil || len(buckets) != 1 {
		t.Fatalf("ListBuckets = %v, %v", buckets, err)
	}
	if calls := f.resolver.calls["example.com"]; calls != 1 {
		t.Fatalf("resolver called %d times; the dial must reuse the checked answer", calls)
	}
	for _, address := range f.dialedAddresses() {
		if address != net.JoinHostPort(testPublicIPv4, "443") {
			t.Fatalf("dialed unpinned address %s", address)
		}
	}
	// The pinned dialer refuses any other host or port it is asked for.
	endpoint, _ := ParseS3BrowseEndpoint("https://example.com", false)
	dial := f.browser.pinnedDial(s3BrowseTarget{S3BrowseEndpoint: endpoint, addrs: addrs(testPublicIPv4)})
	for _, address := range []string{"10.0.0.5:443", "evil.example.net:443", "example.com:8443", "169.254.169.254:80"} {
		if _, err := dial(t.Context(), "tcp", address); !errors.Is(err, errS3BrowseBlockedDial) {
			t.Fatalf("pinned dial to %s not refused: %v", address, err)
		}
	}
	// A blocked pinned address (which the check would never produce) is refused too.
	blocked := f.browser.pinnedDial(s3BrowseTarget{S3BrowseEndpoint: endpoint, addrs: addrs("10.0.0.5")})
	if _, err := blocked(t.Context(), "tcp", "example.com:443"); !errors.Is(err, errS3BrowseBlockedDial) {
		t.Fatalf("blocked pinned address dialed: %v", err)
	}
}

// The production dialer's Control hook re-checks the address the socket really
// connects to. A live loopback listener must never receive the connection.
func TestS3BrowseProductionDialerRefusesBlockedConnectedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var accepted atomic.Bool
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Store(true)
			conn.Close()
		}
	}()
	browser := NewS3Browser()
	conn, err := browser.dial(t.Context(), "tcp", listener.Addr().String())
	if err == nil {
		conn.Close()
		t.Fatal("production dialer connected to loopback")
	}
	if !errors.Is(err, errS3BrowseBlockedDial) {
		t.Fatalf("unexpected dial error type")
	}
	time.Sleep(50 * time.Millisecond)
	if accepted.Load() {
		t.Fatal("loopback listener accepted a connection")
	}
	// The full path classifies it as an endpoint refusal.
	if code := nativeCode(classifyS3BrowseError(t.Context(), fmt.Errorf("wrapped: %w", err))); code != S3EndpointRejectedCode {
		t.Fatalf("code = %q", code)
	}
}

func listBucketsXML(names ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Owner><ID>owner</ID></Owner><Buckets>`)
	for _, name := range names {
		b.WriteString(`<Bucket><Name>` + name + `</Name><CreationDate>2024-01-01T00:00:00.000Z</CreationDate></Bucket>`)
	}
	b.WriteString(`</Buckets></ListAllMyBucketsResult>`)
	return b.String()
}

func TestS3BrowseListBucketsSortedAndCapped(t *testing.T) {
	var names []string
	for i := 1100; i > 0; i-- {
		names = append(names, fmt.Sprintf("bucket-%04d", i))
	}
	// Names the plugin cannot use are not offered; duplicates collapse.
	names = append(names, "Bad_Bucket", "bucket-0001", "ab")
	var sawAuth, sawPath atomic.Bool
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256"))
		sawPath.Store(r.Method == http.MethodGet && r.URL.Path == "/")
		_, _ = w.Write([]byte(listBucketsXML(names...)))
	})
	buckets, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != S3BucketBrowseLimit {
		t.Fatalf("len = %d, want %d", len(buckets), S3BucketBrowseLimit)
	}
	if buckets[0].Name != "bucket-0001" || buckets[999].Name != "bucket-1000" {
		t.Fatalf("not sorted ascending: first %q last %q", buckets[0].Name, buckets[999].Name)
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i-1].Name >= buckets[i].Name {
			t.Fatalf("not strictly ascending at %d", i)
		}
	}
	if !sawAuth.Load() || !sawPath.Load() {
		t.Fatal("request was not a signed path-style ListBuckets")
	}
}

func TestS3BrowseListBucketsEmptyIsNotNil(t *testing.T) {
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(listBucketsXML())) })
	buckets, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"))
	if err != nil || buckets == nil || len(buckets) != 0 {
		t.Fatalf("ListBuckets = %#v, %v", buckets, err)
	}
}

// Upstream error text deliberately quotes the credentials to prove it never escapes.
func upstreamError(status int, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>key %s secret %s token %s</Message><RequestId>r</RequestId></Error>`, code, testS3AccessKey, testS3SecretKey, testS3SessionToken)
	}
}

func TestS3BrowseErrorsAreFixedAndCredentialFree(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	big := strings.Repeat("x", s3BrowseResponseLimit+1)
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		timeout time.Duration
		code    string
		reason  string
	}{
		{"forbidden", upstreamError(403, "AccessDenied"), 0, S3AccessDeniedCode, "denied"},
		{"unauthorized", upstreamError(401, "Unauthorized"), 0, S3AccessDeniedCode, "denied"},
		{"invalid_key_400", upstreamError(400, "InvalidAccessKeyId"), 0, S3AccessDeniedCode, "denied"},
		{"signature_mismatch", upstreamError(403, "SignatureDoesNotMatch"), 0, S3AccessDeniedCode, "denied"},
		{"server_error", upstreamError(500, "InternalError"), 0, S3BackendErrorCode, "status"},
		{"not_found", upstreamError(404, "NoSuchBucket"), 0, S3BackendErrorCode, "status"},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://169.254.169.254/latest/meta-data/")
			w.WriteHeader(http.StatusFound)
		}, 0, S3BackendErrorCode, "redirect"},
		{"timeout", func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}, 200 * time.Millisecond, S3BackendErrorCode, "timeout"},
		{"oversized_declared", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(big)) }, 0, S3BackendErrorCode, "too_large"},
		{"oversized_chunked", func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush()
			for i := 0; i < 5; i++ {
				_, _ = w.Write([]byte(strings.Repeat("y", 1<<20)))
			}
		}, 0, S3BackendErrorCode, "too_large"},
		{"malformed_xml", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`<ListAllMyBucketsResult><Buckets><Bucket><Name>` + testS3AccessKey))
		}, 0, S3BackendErrorCode, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeS3(t, tc.handler)
			if tc.timeout > 0 {
				f.browser.timeout = tc.timeout
			}
			buckets, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: tc.name}, testS3Connection("https://example.com"))
			if err == nil {
				t.Fatalf("ListBuckets succeeded with %d buckets", len(buckets))
			}
			if code := nativeCode(err); code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
			if reason := S3BrowseReason(err); reason != tc.reason {
				t.Fatalf("reason = %q, want %q", reason, tc.reason)
			}
			assertNoS3Credentials(t, "error text", err.Error())
			assertNoS3Credentials(t, "error chain", fmt.Sprintf("%v %+v %#v", err, err, errors.Unwrap(err)))
			for _, address := range f.dialedAddresses() {
				if address != net.JoinHostPort(testPublicIPv4, "443") {
					t.Fatalf("followed to %s", address)
				}
			}
		})
	}
	assertNoS3Credentials(t, "captured log", logs.String())
	connection := testS3Connection("https://example.com")
	assertNoS3Credentials(t, "formatted connection", fmt.Sprintf("%v %+v %#v %s", connection, connection, connection, connection))
	var formatted bytes.Buffer
	slog.New(slog.NewJSONHandler(&formatted, nil)).Info("x", "connection", connection)
	assertNoS3Credentials(t, "slog connection", formatted.String())
}

func TestS3BrowseTLSVerificationStaysOn(t *testing.T) {
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(listBucketsXML("media"))) })
	f.browser.rootCAs = x509.NewCertPool() // the fake certificate is now untrusted
	_, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"))
	if nativeCode(err) != S3BackendErrorCode || S3BrowseReason(err) != "tls" {
		t.Fatalf("untrusted certificate accepted or misclassified: %q/%q", nativeCode(err), S3BrowseReason(err))
	}
}

func TestS3BrowseRefusesBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	f.resolver.answers["internal.example.com"] = [][]netip.Addr{addrs("10.0.0.7")}
	for _, endpoint := range []string{"http://example.com", "https://internal.example.com", "https://127.0.0.1", "https://[::ffff:10.0.0.1]", "https://unknown.example.com"} {
		_, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection(endpoint))
		if nativeCode(err) != S3EndpointRejectedCode {
			t.Fatalf("%s: code %q", endpoint, nativeCode(err))
		}
	}
	_, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a", RestrictPorts: true}, testS3Connection("https://example.com:6443"))
	if nativeCode(err) != S3EndpointRejectedCode {
		t.Fatalf("restricted port: code %q", nativeCode(err))
	}
	if hits.Load() != 0 || len(f.dialedAddresses()) != 0 {
		t.Fatal("a refused endpoint was contacted")
	}
	bad := testS3Connection("https://example.com")
	bad.Region = "US East"
	if _, err := f.browser.ListBuckets(t.Context(), S3BrowseOptions{Actor: "a"}, bad); nativeCode(err) != "invalid_request" {
		t.Fatalf("bad region: %q", nativeCode(err))
	}
}

func listObjectsXML(prefix string, truncated bool, next string, contents []string, commons []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Name>media</Name><Prefix>%s</Prefix><MaxKeys>200</MaxKeys><Delimiter>/</Delimiter><IsTruncated>%t</IsTruncated>`, prefix, truncated)
	if next != "" {
		fmt.Fprintf(&b, `<NextContinuationToken>%s</NextContinuationToken>`, next)
	}
	for _, key := range contents {
		size := 1234
		if strings.HasSuffix(key, "/") {
			size = 0
		}
		fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>%d</Size><ETag>"e"</ETag></Contents>`, key, size)
	}
	for _, p := range commons {
		fmt.Fprintf(&b, `<CommonPrefixes><Prefix>%s</Prefix></CommonPrefixes>`, p)
	}
	b.WriteString(`</ListBucketResult>`)
	return b.String()
}

func TestS3BrowseListFoldersOneLevel(t *testing.T) {
	var query atomic.Value
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		query.Store(r.URL.Path + "?" + r.URL.RawQuery)
		_, _ = w.Write([]byte(listObjectsXML("movies/", false, "",
			[]string{"movies/", "movies/readme.txt", "movies/20s/", "movies/poster.jpg"},
			[]string{"movies/10s/", "movies/20s/", "movies/90s/"})))
	})
	page, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"), "media", "movies/", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []S3Folder{{"10s", "movies/10s/"}, {"20s", "movies/20s/"}, {"90s", "movies/90s/"}}
	if page.Prefix != "movies/" || page.NextCursor != "" || fmt.Sprint(page.Folders) != fmt.Sprint(want) {
		t.Fatalf("page = %+v", page)
	}
	q, _ := query.Load().(string)
	for _, part := range []string{"/media?", "list-type=2", "delimiter=%2F", "max-keys=200", "prefix=movies%2F"} {
		if !strings.Contains(q, part) {
			t.Fatalf("request %q lacks %q", q, part)
		}
	}
	if strings.Contains(q, "continuation-token") {
		t.Fatal("first page sent a continuation token")
	}
}

func TestS3BrowseListFoldersRootMarkersAndFilesOnly(t *testing.T) {
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		// A gateway listing a child directory marker as an object, a nested
		// marker, a file-only level and an unusable child name.
		_, _ = w.Write([]byte(listObjectsXML("", false, "",
			[]string{"movies/", "top.mkv", "deep/nested/"},
			[]string{"movies/", "tv/", "../", "bad\\name/"})))
	})
	page, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"), "media", "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []S3Folder{{"movies", "movies/"}, {"tv", "tv/"}}
	if fmt.Sprint(page.Folders) != fmt.Sprint(want) {
		t.Fatalf("folders = %+v", page.Folders)
	}
	files := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listObjectsXML("movies/10s/", false, "", []string{"movies/10s/", "movies/10s/a.mkv"}, nil)))
	})
	page, err = files.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"), "media", "movies/10s/", "")
	if err != nil || page.Folders == nil || len(page.Folders) != 0 {
		t.Fatalf("files-only level = %#v, %v", page, err)
	}
}

func TestS3BrowseListFoldersPagination(t *testing.T) {
	const token = "1/opaque+token=="
	var seen []string
	var mu sync.Mutex
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Query().Get("continuation-token"))
		mu.Unlock()
		if r.URL.Query().Get("continuation-token") == "" {
			_, _ = w.Write([]byte(listObjectsXML("movies/", true, token, nil, []string{"movies/10s/"})))
			return
		}
		_, _ = w.Write([]byte(listObjectsXML("movies/", false, "", nil, []string{"movies/20s/"})))
	})
	conn := testS3Connection("https://example.com")
	first, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, conn, "media", "movies/", "")
	if err != nil || first.NextCursor != token || len(first.Folders) != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, conn, "media", "movies/", first.NextCursor)
	if err != nil || second.NextCursor != "" || len(second.Folders) != 1 || second.Folders[0].Name != "20s" {
		t.Fatalf("second = %+v, %v", second, err)
	}
	if len(seen) != 2 || seen[1] != token {
		t.Fatalf("continuation tokens sent = %q", seen)
	}
}

func TestS3BrowseListFoldersRejectsInvalidPages(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"truncated_without_token", listObjectsXML("movies/", true, "", nil, []string{"movies/a/"})},
		{"oversized_token", listObjectsXML("movies/", true, strings.Repeat("t", S3BrowseCursorLimit+1), nil, nil)},
		{"foreign_prefix", listObjectsXML("movies/", false, "", nil, []string{"other/a/"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			_, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"), "media", "movies/", "")
			if nativeCode(err) != S3BackendErrorCode {
				t.Fatalf("code = %q", nativeCode(err))
			}
		})
	}
	var many []string
	for i := 0; i <= S3FolderBrowsePageSize; i++ {
		many = append(many, fmt.Sprintf("movies/%04d/", i))
	}
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(listObjectsXML("movies/", false, "", nil, many)))
	})
	if _, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, testS3Connection("https://example.com"), "media", "movies/", ""); nativeCode(err) != S3BackendErrorCode {
		t.Fatalf("over-page folders: code %q", nativeCode(err))
	}
}

func TestS3BrowseListFoldersValidatesInput(t *testing.T) {
	var hits atomic.Int32
	f := newFakeS3(t, func(w http.ResponseWriter, r *http.Request) { hits.Add(1) })
	conn := testS3Connection("https://example.com")
	for _, tc := range []struct{ bucket, prefix, cursor string }{
		{"media", "movies", ""},
		{"media", "movies/../", ""},
		{"media", "../", ""},
		{"media", "./", ""},
		{"media", "a/./b/", ""},
		{"media", "movies\\x/", ""},
		{"media", strings.Repeat("a", 1024) + "/", ""},
		{"media", "bad\x00/", ""},
		{"media", "\xff/", ""},
		{"media", "", strings.Repeat("c", S3BrowseCursorLimit+1)},
		{"media", "", "bad\ncursor"},
		{"Bad_Bucket", "", ""},
		{"", "", ""},
	} {
		if _, err := f.browser.ListFolders(t.Context(), S3BrowseOptions{Actor: "a"}, conn, tc.bucket, tc.prefix, tc.cursor); nativeCode(err) != "invalid_request" {
			t.Fatalf("%q %q accepted: %q", tc.bucket, tc.prefix, nativeCode(err))
		}
	}
	if hits.Load() != 0 {
		t.Fatal("invalid input reached the backend")
	}
	if !ValidS3BrowsePrefix("") || !ValidS3BrowsePrefix("movies/10s/") || !ValidS3BrowsePrefix(strings.Repeat("a", 1023)+"/") {
		t.Fatal("valid prefixes rejected")
	}
}

func orgActor(id int) S3BrowseOptions { return S3BrowseOptions{Actor: fmt.Sprintf("account:%d", id)} }
func platformActor(id int) S3BrowseOptions {
	return S3BrowseOptions{Actor: fmt.Sprintf("account:%d", id), Platform: true}
}

func TestS3BrowseRateLimitTripsAndRecovers(t *testing.T) {
	browser := NewS3Browser()
	now := time.Unix(1_700_000_000, 0)
	browser.limiter.now = func() time.Time { return now }
	for i := 0; i < s3BrowseRateBurst; i++ {
		release, err := browser.admit(orgActor(1))
		if err != nil {
			t.Fatalf("request %d refused", i)
		}
		release()
	}
	_, err := browser.admit(orgActor(1))
	if !errors.Is(err, ErrS3BrowseBusy) || nativeCode(err) != "native_storage_unavailable" {
		t.Fatalf("burst not limited: %v", err)
	}
	// Another administrator is not affected.
	release, err := browser.admit(orgActor(2))
	if err != nil {
		t.Fatal("other actor limited")
	}
	release()
	// 30 per minute: one token every two seconds.
	now = now.Add(2 * time.Second)
	release, err = browser.admit(orgActor(1))
	if err != nil {
		t.Fatal("limit did not recover")
	}
	release()
	if _, err = browser.admit(orgActor(1)); !errors.Is(err, ErrS3BrowseBusy) {
		t.Fatal("recovered more than one token")
	}
	// Steady state over a minute stays at 30.
	allowed := 0
	for i := 0; i < 120; i++ {
		now = now.Add(500 * time.Millisecond)
		if release, err := browser.admit(orgActor(1)); err == nil {
			allowed++
			release()
		}
	}
	if allowed != 30 {
		t.Fatalf("allowed %d in one minute, want 30", allowed)
	}
	// Idle actors are swept, but never one with a request in flight.
	held, err := browser.admit(orgActor(4))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(s3BrowseRateIdle + 2*time.Minute)
	release, _ = browser.admit(orgActor(3))
	release()
	browser.limiter.mu.Lock()
	_, stale := browser.limiter.actors["account:1"]
	_, busy := browser.limiter.actors["account:4"]
	browser.limiter.mu.Unlock()
	if stale || !busy {
		t.Fatalf("sweep: idle kept=%v in-flight kept=%v", stale, busy)
	}
	held()
	held() // release is idempotent
	browser.limiter.mu.Lock()
	defer browser.limiter.mu.Unlock()
	if browser.total != 0 || browser.organization != 0 {
		t.Fatalf("counters leaked: total %d organization %d", browser.total, browser.organization)
	}
}

func TestS3BrowseConcurrencyCaps(t *testing.T) {
	browser := NewS3Browser()
	// One administrator holds at most two slots.
	a1, err1 := browser.admit(orgActor(1))
	a2, err2 := browser.admit(orgActor(1))
	if err1 != nil || err2 != nil {
		t.Fatal("first two slots refused")
	}
	if _, err := browser.admit(orgActor(1)); !errors.Is(err, ErrS3BrowseBusy) {
		t.Fatal("per-actor in-flight cap not enforced")
	}
	// Organization actors together hold at most three; the fourth is reserved.
	b1, err := browser.admit(orgActor(2))
	if err != nil {
		t.Fatal("third organization slot refused")
	}
	if _, err := browser.admit(orgActor(3)); !errors.Is(err, ErrS3BrowseBusy) {
		t.Fatal("organization actors took the platform reserve")
	}
	p1, err := browser.admit(platformActor(9))
	if err != nil {
		t.Fatal("platform reserve unavailable")
	}
	// Server-wide cap of four.
	if _, err := browser.admit(platformActor(10)); !errors.Is(err, ErrS3BrowseBusy) {
		t.Fatal("global cap not enforced")
	}
	// Refusals by a cap consume no token: release and the same actor proceeds.
	a1()
	a3, err := browser.admit(orgActor(1))
	if err != nil {
		t.Fatal("slot not returned to actor")
	}
	for _, r := range []func(){a2, a3, b1, p1} {
		r()
	}
	// Platform actors may use every slot, subject to their own cap of two each.
	var all []func()
	for i := 0; i < s3BrowseConcurrency; i++ {
		r, err := browser.admit(platformActor(20 + i/2))
		if err != nil {
			t.Fatalf("platform slot %d refused", i)
		}
		all = append(all, r)
	}
	for _, r := range all {
		r()
	}
}

func TestActorRateLimiter(t *testing.T) {
	limiter := NewActorRateLimiter(10, 3)
	now := time.Unix(1_700_000_000, 0)
	limiter.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !limiter.Allow("account:1") {
			t.Fatalf("burst request %d refused", i)
		}
	}
	if limiter.Allow("account:1") || !limiter.Allow("account:2") {
		t.Fatal("limit not per actor")
	}
	now = now.Add(6 * time.Second) // 10 per minute: one token every six seconds
	if !limiter.Allow("account:1") || limiter.Allow("account:1") {
		t.Fatal("limit did not recover by exactly one token")
	}
	var nilLimiter *ActorRateLimiter
	if nilLimiter.Allow("x") {
		t.Fatal("nil limiter must refuse")
	}
}

func TestS3BrowseExtendedBlocklistAndHostAddresses(t *testing.T) {
	for _, ip := range []string{"2001::1", "2001:0:4136:e378:8000:63bf:3fff:fdd2", "::ffff:0:a00:1", "fec0::1", "192.88.99.1",
		"198.18.0.1", "100::1", "2001:db8::1", "192.0.2.1", "198.51.100.1", "203.0.113.1"} {
		if !S3BrowseAddressBlocked(netip.MustParseAddr(ip)) {
			t.Fatalf("%s not blocked", ip)
		}
	}
	for addr := range s3BrowseHostAddrs() {
		if !S3BrowseAddressBlocked(addr) {
			t.Fatalf("host interface address %s not blocked", addr)
		}
	}
	if len(s3BrowseHostAddrs()) == 0 {
		t.Skip("no interface addresses visible")
	}
}
