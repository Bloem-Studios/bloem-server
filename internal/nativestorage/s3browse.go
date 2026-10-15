package nativestorage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"golang.org/x/time/rate"

	"github.com/Silo-Server/silo-server/internal/catalog"
)

// S3 browsing lets an administrator pick a bucket and folder while adding an
// S3 source. It is stateless: the connection is used for one request and is
// never stored, logged or returned. Because the endpoint is caller-supplied,
// every request is guarded against server-side request forgery:
//
//   - the endpoint must be an https origin (no userinfo, path, query or
//     fragment) and, when the caller asks for it, use a well-known S3 port;
//   - its host is resolved here, and the request is refused if ANY address is
//     loopback, private, link-local, unique-local, CGNAT, multicast,
//     unspecified, reserved or an IPv6 form embedding such an IPv4 address;
//   - the connection is pinned to the addresses that were checked, so DNS
//     rebinding between check and use cannot reach another address, and the
//     production dialer re-checks the address it actually connects to;
//   - redirects are never followed, TLS verification stays on, the whole
//     request is bounded in time and the response body in size.
//
// Errors are fixed codes; no upstream text, response body, resolved address or
// timing detail ever leaves this file.

const (
	// S3BucketBrowseLimit caps the bucket names returned by ListBuckets.
	S3BucketBrowseLimit = 1000
	// S3FolderBrowsePageSize is the MaxKeys of one folder listing page.
	S3FolderBrowsePageSize = 200
	// S3BrowseCursorLimit bounds the opaque continuation token in bytes.
	S3BrowseCursorLimit = 4096

	s3BrowseTimeout       = 15 * time.Second
	s3BrowseResolveLimit  = 5 * time.Second
	s3BrowseDialTimeout   = 10 * time.Second
	s3BrowseResponseLimit = 4 << 20
	s3BrowseConcurrency   = 4
	// Organization actors together may hold at most three of the four slots,
	// so one slot stays available to platform administrators.
	s3BrowseOrganizationInFlight = 3
	// One administrator may hold at most two slots.
	s3BrowsePerActorInFlight = 2
	// 30 requests per minute per administrator, with a burst of 10.
	s3BrowseRatePerSecond = 0.5
	s3BrowseRateBurst     = 10
	s3BrowseRateIdle      = 10 * time.Minute
)

// Fixed public error codes of the browse operations.
const (
	S3EndpointRejectedCode = "s3_endpoint_rejected"
	S3AccessDeniedCode     = "s3_access_denied"
	S3BackendErrorCode     = "s3_backend_error"
)

// ErrS3BrowseBusy marks a refusal by the per-administrator rate limit or the
// server-wide concurrency cap. It carries the native_storage_unavailable code.
var ErrS3BrowseBusy = errors.New("s3 browse busy")

// S3BrowseFailure is the internal cause of a browse error. Reason is one of a
// fixed set of words for audit logs; it never contains upstream text.
type S3BrowseFailure struct{ Reason string }

func (f *S3BrowseFailure) Error() string { return "s3 browse " + f.Reason }

// S3BrowseReason returns the fixed reason word of a browse error, or "".
func S3BrowseReason(err error) string {
	var failure *S3BrowseFailure
	if errors.As(err, &failure) && failure != nil {
		return failure.Reason
	}
	if errors.Is(err, ErrS3BrowseBusy) {
		return "busy"
	}
	return ""
}

func s3BrowseError(code, reason string) error {
	return &catalog.NativeOnboardingError{Code: code, Cause: &S3BrowseFailure{Reason: reason}}
}

// S3BrowseConnection is the caller-supplied connection. It is held only in
// memory for one request; its formatting methods never print credentials.
type S3BrowseConnection struct {
	Endpoint     string
	Region       string
	AccessKey    string
	SecretKey    string
	SessionToken string
}

func (S3BrowseConnection) String() string       { return "S3BrowseConnection{redacted}" }
func (c S3BrowseConnection) GoString() string   { return c.String() }
func (S3BrowseConnection) LogValue() slog.Value { return slog.StringValue("redacted") }

// Format covers every verb, including %+v and %#v, which would otherwise print fields.
func (c S3BrowseConnection) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, c.String()) }

// S3BrowseOptions selects the per-request policy.
type S3BrowseOptions struct {
	// Actor keys the per-administrator rate limit.
	Actor string
	// RestrictPorts limits the endpoint port to S3BrowseOrganizationPorts.
	RestrictPorts bool
	// Platform marks a platform administrator, who may use the reserved
	// concurrency slot. Anything else counts as an organization actor.
	Platform bool
}

// S3BrowseOrganizationPorts are the only endpoint ports allowed when
// RestrictPorts is set (organization administrators).
var S3BrowseOrganizationPorts = []int{443, 7480, 8443, 9000, 9443}

// S3Bucket is one bucket name.
type S3Bucket struct {
	Name string `json:"name"`
}

// S3Folder is one immediate child folder of a listed prefix.
type S3Folder struct {
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

// S3FolderPage is one page of immediate child folders.
type S3FolderPage struct {
	Prefix     string     `json:"prefix"`
	Folders    []S3Folder `json:"folders"`
	NextCursor string     `json:"next_cursor"`
}

type s3BrowseResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// S3Browser performs stateless, guarded S3 listings.
type S3Browser struct {
	resolver s3BrowseResolver
	// dial connects to an already validated "ip:port". Production refuses a
	// blocked connected address again in the dialer's Control hook.
	dial    func(ctx context.Context, network, address string) (net.Conn, error)
	rootCAs *x509.CertPool
	timeout time.Duration

	// limiter keys the per-administrator token bucket and in-flight count;
	// its mutex also guards total and organization.
	limiter      *ActorRateLimiter
	total        int
	organization int
}

// ActorRateLimiter is an in-memory token bucket per actor key with an
// in-flight count. Idle entries are swept on access, without a goroutine.
// Limits are per process.
type ActorRateLimiter struct {
	limit  rate.Limit
	burst  int
	now    func() time.Time
	mu     sync.Mutex
	actors map[string]*actorBucket
	swept  time.Time
}

type actorBucket struct {
	limiter  *rate.Limiter
	used     time.Time
	inflight int
}

// NewActorRateLimiter allows perMinute requests per actor with the given burst.
func NewActorRateLimiter(perMinute float64, burst int) *ActorRateLimiter {
	return &ActorRateLimiter{limit: rate.Limit(perMinute / 60), burst: burst, now: time.Now, actors: map[string]*actorBucket{}}
}

// bucketLocked returns the actor's bucket; l.mu must be held.
func (l *ActorRateLimiter) bucketLocked(actor string, now time.Time) *actorBucket {
	if now.Sub(l.swept) > time.Minute {
		for key, bucket := range l.actors {
			if bucket.inflight == 0 && now.Sub(bucket.used) > s3BrowseRateIdle {
				delete(l.actors, key)
			}
		}
		l.swept = now
	}
	bucket := l.actors[actor]
	if bucket == nil {
		bucket = &actorBucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.actors[actor] = bucket
	}
	bucket.used = now
	return bucket
}

// Allow takes one token for the actor, or reports that the actor is limited.
func (l *ActorRateLimiter) Allow(actor string) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	return l.bucketLocked(actor, now).limiter.AllowN(now, 1)
}

// NewS3Browser returns a browser using the system resolver and roots.
func NewS3Browser() *S3Browser {
	dialer := &net.Dialer{Timeout: s3BrowseDialTimeout, Control: s3BrowseDialControl}
	return &S3Browser{
		resolver: net.DefaultResolver,
		dial:     dialer.DialContext,
		timeout:  s3BrowseTimeout,
		limiter:  NewActorRateLimiter(s3BrowseRatePerSecond*60, s3BrowseRateBurst),
	}
}

// s3BrowseDialControl runs on the address the socket actually connects to.
func s3BrowseDialControl(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil || S3BrowseAddressBlocked(addrPort.Addr()) {
		return errS3BrowseBlockedDial
	}
	return nil
}

var errS3BrowseBlockedDial = errors.New("s3 browse dial refused")

var s3BrowseBlockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, cidr := range []string{
		// IPv4
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15",
		"224.0.0.0/4", "240.0.0.0/4",
		// 6to4 relay anycast and the documentation ranges.
		"192.88.99.0/24", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
		// IPv6
		"::1/128", "::/128", "fc00::/7", "fe80::/10", "ff00::/8",
		// IPv6 forms that can embed or translate to an IPv4 address: IPv4-compatible,
		// NAT64 (well-known and local-use) and 6to4. Refused outright rather than decoded.
		"::/96", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16",
		// Teredo (embeds IPv4), IPv4-translated (SIIT, which Unmap does not
		// decode), deprecated site-local, discard-only and documentation.
		"2001::/32", "::ffff:0:0:0/96", "fec0::/10", "100::/64", "2001:db8::/32",
	} {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}()

// S3BrowseAddressBlocked reports whether an address may not be contacted.
// IPv4-mapped IPv6 addresses are checked as IPv4; zoned addresses are refused.
func S3BrowseAddressBlocked(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	addr = addr.Unmap()
	for _, prefix := range s3BrowseBlockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	// The host's own addresses, including a public one that could hairpin
	// past an upstream firewall.
	if s3BrowseHostAddrs()[addr] {
		return true
	}
	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified()
}

// s3BrowseHostAddrs lists this host's interface addresses once, at first use.
var s3BrowseHostAddrs = sync.OnceValue(func() map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		if prefix, err := netip.ParsePrefix(a.String()); err == nil {
			out[prefix.Addr().Unmap()] = true
		} else if addr, err := netip.ParseAddr(a.String()); err == nil {
			out[addr.Unmap()] = true
		}
	}
	return out
})

// S3BrowseEndpoint is a validated endpoint origin.
type S3BrowseEndpoint struct {
	Host string // hostname or IP literal, without brackets
	Port int
	Base string // https origin passed to the SDK
}

// ParseS3BrowseEndpoint applies the S3 plugin's endpoint shape rules, except
// that HTTPS is mandatory and the endpoint is required. It does no I/O.
func ParseS3BrowseEndpoint(raw string, restrictPorts bool) (S3BrowseEndpoint, error) {
	return parseGuardedURL(raw, restrictPorts, false)
}

// ParseServiceBaseURL applies the same rules to a service base URL (for
// example Bookwarehouse base_url), which may carry a path.
func ParseServiceBaseURL(raw string, restrictPorts bool) (S3BrowseEndpoint, error) {
	return parseGuardedURL(raw, restrictPorts, true)
}

func parseGuardedURL(raw string, restrictPorts, allowPath bool) (S3BrowseEndpoint, error) {
	reject := func(reason string) (S3BrowseEndpoint, error) {
		return S3BrowseEndpoint{}, s3BrowseError(S3EndpointRejectedCode, reason)
	}
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) || strings.IndexFunc(raw, unicode.IsControl) >= 0 ||
		strings.ContainsAny(raw, "#\\ ") {
		return reject("shape")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.Host == "" || u.User != nil || u.ForceQuery ||
		u.RawQuery != "" || u.Fragment != "" || (!allowPath && u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return reject("shape")
	}
	host := u.Hostname()
	if host == "" || len(host) > 253 || strings.Contains(host, "%") {
		return reject("shape")
	}
	port := 443
	if u.Port() != "" || strings.HasSuffix(u.Host, ":") {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return reject("port")
		}
		port = n
	}
	if restrictPorts && !s3BrowseOrganizationPort(port) {
		return reject("port")
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if S3BrowseAddressBlocked(addr) {
			return reject("address")
		}
		host = addr.String()
	} else {
		host = strings.ToLower(host)
	}
	origin := url.URL{Scheme: "https", Host: net.JoinHostPort(host, strconv.Itoa(port))}
	return S3BrowseEndpoint{Host: host, Port: port, Base: origin.String()}, nil
}

// S3BrowseEndpointHost returns the lowercased host of an endpoint for audit
// logs, even for a refused endpoint, or "" when it is not a plain host name or
// IP literal. It does no I/O and never returns userinfo, path or query.
func S3BrowseEndpointHost(raw string) string {
	if len(raw) > 2048 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || len(host) > 253 || strings.Trim(host, "abcdefghijklmnopqrstuvwxyz0123456789.-:") != "" {
		return ""
	}
	return host
}

func s3BrowseOrganizationPort(port int) bool {
	for _, p := range S3BrowseOrganizationPorts {
		if p == port {
			return true
		}
	}
	return false
}

// CheckS3SourceEndpoint applies the browse endpoint guard (shape, optional
// port restriction, every resolved address) to the endpoint of an S3 source
// configuration before a plugin process is started with it. An empty endpoint
// selects the AWS default and is allowed. This is a pre-check only: the plugin
// process resolves and dials by itself, so its connection cannot be pinned.
func CheckS3SourceEndpoint(ctx context.Context, raw string, restrictPorts bool) error {
	return checkGuardedURL(ctx, net.DefaultResolver, raw, restrictPorts, false)
}

// CheckServiceBaseURL is CheckS3SourceEndpoint for a service base URL that may
// carry a path (Bookwarehouse base_url). An empty value is left to the plugin.
func CheckServiceBaseURL(ctx context.Context, raw string, restrictPorts bool) error {
	return checkGuardedURL(ctx, net.DefaultResolver, raw, restrictPorts, true)
}

func checkGuardedURL(ctx context.Context, resolver s3BrowseResolver, raw string, restrictPorts, allowPath bool) error {
	if raw == "" {
		return nil
	}
	endpoint, err := parseGuardedURL(raw, restrictPorts, allowPath)
	if err != nil {
		return err
	}
	_, err = (&S3Browser{resolver: resolver}).resolveS3BrowseTarget(ctx, endpoint)
	return err
}

// s3BrowseTarget is an endpoint whose addresses were all checked.
type s3BrowseTarget struct {
	S3BrowseEndpoint
	addrs []netip.Addr
}

// resolveS3BrowseTarget refuses the endpoint if any resolved address is blocked.
func (b *S3Browser) resolveS3BrowseTarget(ctx context.Context, endpoint S3BrowseEndpoint) (s3BrowseTarget, error) {
	target := s3BrowseTarget{S3BrowseEndpoint: endpoint}
	if addr, err := netip.ParseAddr(endpoint.Host); err == nil {
		if S3BrowseAddressBlocked(addr) {
			return s3BrowseTarget{}, s3BrowseError(S3EndpointRejectedCode, "address")
		}
		target.addrs = []netip.Addr{addr.Unmap()}
		return target, nil
	}
	lookup, cancel := context.WithTimeout(ctx, s3BrowseResolveLimit)
	defer cancel()
	addrs, err := b.resolver.LookupNetIP(lookup, "ip", endpoint.Host)
	if err != nil || len(addrs) == 0 {
		return s3BrowseTarget{}, s3BrowseError(S3EndpointRejectedCode, "dns")
	}
	seen := map[netip.Addr]bool{}
	for _, addr := range addrs {
		if S3BrowseAddressBlocked(addr) {
			return s3BrowseTarget{}, s3BrowseError(S3EndpointRejectedCode, "address")
		}
		addr = addr.Unmap()
		if !seen[addr] {
			seen[addr] = true
			target.addrs = append(target.addrs, addr)
		}
	}
	return target, nil
}

// pinnedDial ignores DNS: it connects only to the checked addresses, and only
// for the validated host and port.
func (b *S3Browser) pinnedDial(target s3BrowseTarget) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, target.Host) || port != strconv.Itoa(target.Port) {
			return nil, errS3BrowseBlockedDial
		}
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, errS3BrowseBlockedDial
		}
		var last error = errS3BrowseBlockedDial
		for _, addr := range target.addrs {
			if S3BrowseAddressBlocked(addr) {
				return nil, errS3BrowseBlockedDial
			}
			conn, err := b.dial(ctx, "tcp", netip.AddrPortFrom(addr, uint16(target.Port)).String())
			if err == nil {
				return conn, nil
			}
			if errors.Is(err, errS3BrowseBlockedDial) {
				return nil, err
			}
			last = err
		}
		return nil, last
	}
}

var errS3BrowseTooLarge = errors.New("s3 browse response too large")

type s3BrowseLimitedTransport struct{ inner http.RoundTripper }

func (t s3BrowseLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > s3BrowseResponseLimit {
		resp.Body.Close()
		return nil, errS3BrowseTooLarge
	}
	resp.Body = &s3BrowseLimitedBody{body: resp.Body, remaining: s3BrowseResponseLimit}
	return resp, nil
}

type s3BrowseLimitedBody struct {
	body      io.ReadCloser
	remaining int64
}

func (b *s3BrowseLimitedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		// Probe one byte: exactly the limit is acceptable, more is not.
		var probe [1]byte
		n, err := b.body.Read(probe[:])
		if n > 0 {
			return 0, errS3BrowseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.body.Read(p)
	b.remaining -= int64(n)
	return n, err
}

func (b *s3BrowseLimitedBody) Close() error { return b.body.Close() }

// client builds a one-request S3 client pinned to the checked target.
func (b *S3Browser) client(target s3BrowseTarget, conn S3BrowseConnection) (*s3.Client, func()) {
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            b.pinnedDial(target),
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: b.rootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  b.timeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     true,
		MaxIdleConns:           1,
		IdleConnTimeout:        time.Second,
	}
	httpClient := &http.Client{
		Transport:     s3BrowseLimitedTransport{inner: transport},
		Timeout:       b.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client := s3.New(s3.Options{
		Region:                      conn.Region,
		BaseEndpoint:                aws.String(target.Base),
		Credentials:                 credentials.NewStaticCredentialsProvider(conn.AccessKey, conn.SecretKey, conn.SessionToken),
		UsePathStyle:                true,
		HTTPClient:                  httpClient,
		Retryer:                     aws.NopRetryer{},
		Logger:                      logging.Nop{},
		ClientLogMode:               0,
		DisableS3ExpressSessionAuth: aws.Bool(true),
		RequestChecksumCalculation:  aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation:  aws.ResponseChecksumValidationWhenRequired,
	})
	return client, transport.CloseIdleConnections
}

// admit applies, in order, the per-administrator in-flight cap (2), the
// server-wide cap (4, of which organization actors may hold 3) and the
// per-administrator token bucket. A refusal by a cap consumes no token.
func (b *S3Browser) admit(opts S3BrowseOptions) (func(), error) {
	busy := &catalog.NativeOnboardingError{Code: "native_storage_unavailable", Cause: ErrS3BrowseBusy}
	l := b.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket := l.bucketLocked(opts.Actor, now)
	if bucket.inflight >= s3BrowsePerActorInFlight || b.total >= s3BrowseConcurrency ||
		(!opts.Platform && b.organization >= s3BrowseOrganizationInFlight) {
		return nil, busy
	}
	if !bucket.limiter.AllowN(now, 1) {
		return nil, busy
	}
	bucket.inflight++
	b.total++
	if !opts.Platform {
		b.organization++
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			bucket.inflight--
			b.total--
			if !opts.Platform {
				b.organization--
			}
		})
	}, nil
}

// prepare validates, admits and resolves; the caller must call release.
func (b *S3Browser) prepare(ctx context.Context, opts S3BrowseOptions, conn S3BrowseConnection) (*s3.Client, context.Context, func(), error) {
	if b == nil || b.resolver == nil || b.dial == nil || b.limiter == nil {
		return nil, nil, nil, &catalog.NativeOnboardingError{Code: "native_storage_unavailable"}
	}
	if err := ValidateS3BrowseCredentials(conn); err != nil {
		return nil, nil, nil, err
	}
	endpoint, err := ParseS3BrowseEndpoint(conn.Endpoint, opts.RestrictPorts)
	if err != nil {
		return nil, nil, nil, err
	}
	release, err := b.admit(opts)
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	target, err := b.resolveS3BrowseTarget(ctx, endpoint)
	if err != nil {
		cancel()
		release()
		return nil, nil, nil, err
	}
	client, closeIdle := b.client(target, conn)
	return client, ctx, func() { closeIdle(); cancel(); release() }, nil
}

var (
	s3BrowseBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	s3BrowseRegionPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

func s3BrowseText(value string, limit int) bool {
	return utf8.ValidString(value) && len(value) <= limit && strings.IndexFunc(value, unicode.IsControl) < 0
}

// ValidateS3BrowseCredentials applies the S3 plugin's region and credential
// rules. It returns invalid_request; the endpoint is checked separately.
func ValidateS3BrowseCredentials(conn S3BrowseConnection) error {
	if conn.Endpoint == "" || !s3BrowseRegionPattern.MatchString(conn.Region) || conn.AccessKey == "" || conn.SecretKey == "" ||
		!s3BrowseText(conn.AccessKey, 1024) || !s3BrowseText(conn.SecretKey, 4096) || !s3BrowseText(conn.SessionToken, 8192) {
		return &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	return nil
}

// ValidS3BrowseBucket reports whether a bucket name is usable by the plugin.
func ValidS3BrowseBucket(name string) bool { return s3BrowseBucketPattern.MatchString(name) }

// ValidS3BrowsePrefix applies the plugin's prefix rules: UTF-8, at most 1024
// bytes, no control characters or backslash, no "." or ".." segment, and
// empty or slash terminated.
func ValidS3BrowsePrefix(prefix string) bool {
	if !s3BrowseText(prefix, 1024) || strings.Contains(prefix, "\\") || (prefix != "" && !strings.HasSuffix(prefix, "/")) {
		return false
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// ValidS3BrowseCursor bounds an opaque continuation token.
func ValidS3BrowseCursor(cursor string) bool {
	return s3BrowseText(cursor, S3BrowseCursorLimit)
}

// ListBuckets returns at most S3BucketBrowseLimit usable bucket names, sorted.
func (b *S3Browser) ListBuckets(ctx context.Context, opts S3BrowseOptions, conn S3BrowseConnection) ([]S3Bucket, error) {
	client, ctx, release, err := b.prepare(ctx, opts, conn)
	if err != nil {
		return nil, err
	}
	defer release()
	out, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, classifyS3BrowseError(ctx, err)
	}
	names := map[string]bool{}
	for _, bucket := range out.Buckets {
		// Names the plugin cannot use are not offered.
		if bucket.Name != nil && ValidS3BrowseBucket(*bucket.Name) {
			names[*bucket.Name] = true
		}
	}
	buckets := make([]S3Bucket, 0, len(names))
	for name := range names {
		buckets = append(buckets, S3Bucket{Name: name})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].Name < buckets[j].Name })
	if len(buckets) > S3BucketBrowseLimit {
		buckets = buckets[:S3BucketBrowseLimit]
	}
	return buckets, nil
}

// ListFolders returns one page of the immediate child folders of prefix.
// Files are never returned. A page may hold no folders and still continue.
func (b *S3Browser) ListFolders(ctx context.Context, opts S3BrowseOptions, conn S3BrowseConnection, bucket, prefix, cursor string) (S3FolderPage, error) {
	if !ValidS3BrowseBucket(bucket) || !ValidS3BrowsePrefix(prefix) || !ValidS3BrowseCursor(cursor) {
		return S3FolderPage{}, &catalog.NativeOnboardingError{Code: "invalid_request"}
	}
	client, ctx, release, err := b.prepare(ctx, opts, conn)
	if err != nil {
		return S3FolderPage{}, err
	}
	defer release()
	input := &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Delimiter: aws.String("/"), MaxKeys: aws.Int32(S3FolderBrowsePageSize)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if cursor != "" {
		input.ContinuationToken = aws.String(cursor)
	}
	out, err := client.ListObjectsV2(ctx, input)
	if err != nil {
		return S3FolderPage{}, classifyS3BrowseError(ctx, err)
	}
	seen := map[string]bool{}
	page := S3FolderPage{Prefix: prefix, Folders: []S3Folder{}}
	add := func(child string) error {
		if !strings.HasPrefix(child, prefix) {
			return s3BrowseError(S3BackendErrorCode, "invalid_page")
		}
		name, ok := strings.CutSuffix(child[len(prefix):], "/")
		// Only one level, with a name the prefix rules can select again.
		if !ok || name == "" || strings.Contains(name, "/") || !ValidS3BrowsePrefix(child) || seen[child] {
			return nil
		}
		seen[child] = true
		page.Folders = append(page.Folders, S3Folder{Name: name, Prefix: child})
		return nil
	}
	for _, common := range out.CommonPrefixes {
		if common.Prefix == nil {
			continue
		}
		if err := add(*common.Prefix); err != nil {
			return S3FolderPage{}, err
		}
	}
	// Directory markers are zero-byte keys ending in "/". The folder's own
	// marker equals prefix and is skipped; a child marker a gateway lists as
	// an object instead of a common prefix is merged without duplication.
	for _, object := range out.Contents {
		if object.Key == nil || *object.Key == prefix || !strings.HasSuffix(*object.Key, "/") || aws.ToInt64(object.Size) != 0 {
			continue
		}
		if err := add(*object.Key); err != nil {
			return S3FolderPage{}, err
		}
	}
	if len(page.Folders) > S3FolderBrowsePageSize {
		return S3FolderPage{}, s3BrowseError(S3BackendErrorCode, "invalid_page")
	}
	sort.Slice(page.Folders, func(i, j int) bool { return page.Folders[i].Prefix < page.Folders[j].Prefix })
	if aws.ToBool(out.IsTruncated) {
		next := aws.ToString(out.NextContinuationToken)
		if next == "" || !ValidS3BrowseCursor(next) || next == cursor {
			return S3FolderPage{}, s3BrowseError(S3BackendErrorCode, "invalid_page")
		}
		page.NextCursor = next
	}
	return page, nil
}

// Backend error codes that mean the credentials were refused.
var s3BrowseDeniedCodes = map[string]bool{
	"AccessDenied": true, "InvalidAccessKeyId": true, "SignatureDoesNotMatch": true, "InvalidToken": true,
	"ExpiredToken": true, "TokenRefreshRequired": true, "AllAccessDisabled": true, "AccountProblem": true,
	"InvalidSecurity": true, "InvalidClientTokenId": true, "UnrecognizedClientException": true,
}

// classifyS3BrowseError keeps only a fixed code and reason; the SDK error,
// which may quote the backend, is discarded.
func classifyS3BrowseError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errS3BrowseBlockedDial):
		return s3BrowseError(S3EndpointRejectedCode, "address")
	case errors.Is(err, errS3BrowseTooLarge):
		return s3BrowseError(S3BackendErrorCode, "too_large")
	}
	var response *awshttp.ResponseError
	status := 0
	if errors.As(err, &response) && response != nil && response.Response != nil {
		status = response.HTTPStatusCode()
	}
	var api smithy.APIError
	if status == http.StatusUnauthorized || status == http.StatusForbidden || (errors.As(err, &api) && api != nil && s3BrowseDeniedCodes[api.ErrorCode()]) {
		return s3BrowseError(S3AccessDeniedCode, "denied")
	}
	if status >= 300 && status < 400 {
		return s3BrowseError(S3BackendErrorCode, "redirect")
	}
	if status >= 400 || (status != 0 && status < 200) {
		return s3BrowseError(S3BackendErrorCode, "status")
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		return s3BrowseError(S3BackendErrorCode, "timeout")
	}
	// A successful status whose body cannot be decoded is malformed XML.
	var deserialize *smithy.DeserializationError
	if errors.As(err, &deserialize) || status != 0 {
		return s3BrowseError(S3BackendErrorCode, "decode")
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var verification *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &verification) || errors.As(err, &record) {
		return s3BrowseError(S3BackendErrorCode, "tls")
	}
	return s3BrowseError(S3BackendErrorCode, "request")
}
