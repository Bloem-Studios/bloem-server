package compatgateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Status is the gateway's view of one application's lifecycle state. It is
// deliberately narrow: the gateway consumes state, it never mutates it. The
// enrollment/trust service (internal/compatapp) is the authority behind a
// StateProvider.
type Status struct {
	// Known reports whether the application has an enrollment record at all.
	Known bool
	// Enrolled reports a completed enrollment handshake.
	Enrolled bool
	// Enabled reports the administrator's explicit routing decision. Starting
	// a container never sets this; an admin does, after verification.
	Enabled bool
	// Revoked reports that the application's credentials were revoked.
	Revoked bool
	// Healthy reports the most recent health verdict.
	Healthy bool
	// APICompatible reports a successful private-API range handshake.
	APICompatible bool
	// Endpoint is the application's private base URL.
	Endpoint *url.URL
}

// Routable reports whether the gateway may forward requests to the
// application: enrolled, enabled, not revoked, healthy, API-compatible, and
// reachable at a known endpoint. Anything less answers unavailable.
func (s Status) Routable() bool {
	return s.Known && s.Enrolled && s.Enabled && !s.Revoked && s.Healthy && s.APICompatible && s.Endpoint != nil
}

// StateProvider supplies the current lifecycle state for an application.
// Implementations must fail closed: an error means "not routable".
type StateProvider interface {
	ApplicationStatus(ctx context.Context, kind AppKind) (Status, error)
}

const (
	// internalIdentityHeader carries the gateway-signed request identity. Any
	// caller-supplied value is discarded before forwarding.
	internalIdentityHeader = "X-Vondel-Internal-Identity"
	// traceHeader propagates one trace identifier across the boundary.
	traceHeader = "X-Vondel-Trace-Id"

	defaultMaxRequestBytes  = int64(32 << 20)
	defaultUpstreamTimeout  = 90 * time.Second
	defaultFailureThreshold = 5
	defaultCircuitCooldown  = 30 * time.Second
	unavailableRetryAfter   = "30"
)

// Config configures the gateway.
type Config struct {
	// States resolves application lifecycle state. Required.
	States StateProvider
	// Transport dials companions. Defaults to http.DefaultTransport.
	Transport http.RoundTripper
	// IdentitySecret signs the internal request identity header. Required.
	IdentitySecret []byte
	// MaxRequestBytes bounds inbound request bodies. Defaults to 32 MiB.
	MaxRequestBytes int64
	// UpstreamTimeout bounds one proxied exchange. Defaults to 90s.
	UpstreamTimeout time.Duration
	// FailureThreshold is the consecutive transport-failure count that opens
	// an application's circuit. Defaults to 5.
	FailureThreshold int
	// CircuitCooldown is how long an open circuit refuses to dial. Defaults
	// to 30s.
	CircuitCooldown time.Duration
	// Now is the clock, for tests. Defaults to time.Now.
	Now func() time.Time
}

// Gateway forwards the fixed compatibility route families to enrolled,
// enabled, healthy, API-compatible applications and nothing else. It holds no
// runtime route state: the table is package-level, compile-time data.
type Gateway struct {
	states           StateProvider
	transport        http.RoundTripper
	identitySecret   []byte
	maxRequestBytes  int64
	upstreamTimeout  time.Duration
	failureThreshold int
	circuitCooldown  time.Duration
	now              func() time.Time

	mu       sync.Mutex
	breakers map[AppKind]*breaker
}

type breaker struct {
	consecutiveFailures int
	openUntil           time.Time
}

// New builds a Gateway.
func New(cfg Config) *Gateway {
	gateway := &Gateway{
		states:           cfg.States,
		transport:        cfg.Transport,
		identitySecret:   cfg.IdentitySecret,
		maxRequestBytes:  cfg.MaxRequestBytes,
		upstreamTimeout:  cfg.UpstreamTimeout,
		failureThreshold: cfg.FailureThreshold,
		circuitCooldown:  cfg.CircuitCooldown,
		now:              cfg.Now,
		breakers:         map[AppKind]*breaker{},
	}
	if gateway.transport == nil {
		gateway.transport = http.DefaultTransport
	}
	if gateway.maxRequestBytes <= 0 {
		gateway.maxRequestBytes = defaultMaxRequestBytes
	}
	if gateway.upstreamTimeout <= 0 {
		gateway.upstreamTimeout = defaultUpstreamTimeout
	}
	if gateway.failureThreshold <= 0 {
		gateway.failureThreshold = defaultFailureThreshold
	}
	if gateway.circuitCooldown <= 0 {
		gateway.circuitCooldown = defaultCircuitCooldown
	}
	if gateway.now == nil {
		gateway.now = time.Now
	}
	return gateway
}

// ServeHTTP forwards a request the fixed table owns. Paths outside the table
// are 404: they never fall through into another authentication surface, and
// the gateway never answers for native routes.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := MatchPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	traceID := requestTraceID(r)
	w.Header().Set(traceHeader, traceID)

	status, err := g.applicationStatus(r.Context(), route.App)
	if err != nil || !status.Routable() {
		writeUnavailable(w, route.App)
		return
	}

	if g.circuitOpen(route.App) {
		writeUnavailable(w, route.App)
		return
	}

	if r.ContentLength > g.maxRequestBytes {
		writeGatewayError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the compatibility gateway limit")
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, g.maxRequestBytes)
	}

	ctx, cancel := context.WithTimeout(r.Context(), g.upstreamTimeout)
	defer cancel()

	proxy := &httputil.ReverseProxy{
		Transport: g.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = status.Endpoint.Scheme
			out.URL.Host = status.Endpoint.Host
			if route.StripPrefix {
				out.URL.Path = strippedPath(out.URL.Path)
				out.URL.RawPath = ""
			}
			// The internal identity is minted here and only here; whatever the
			// caller sent was already dropped from pr.Out by SetXForwarded and
			// the explicit delete below.
			out.Header.Del(internalIdentityHeader)
			out.Header.Set(internalIdentityHeader, g.signIdentity(route.App, traceID))
			out.Header.Set(traceHeader, traceID)
			pr.SetXForwarded()
		},
		ModifyResponse: func(resp *http.Response) error {
			// Any upstream response — even an upstream 5xx — is a live
			// companion answering its own protocol, so the circuit resets.
			g.recordSuccess(route.App)
			return rewriteRedirect(resp, route, status.Endpoint)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			g.recordFailure(route.App)
			var maxBytes *http.MaxBytesError
			switch {
			case errors.As(err, &maxBytes):
				writeGatewayError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the compatibility gateway limit")
			case errors.Is(err, context.DeadlineExceeded):
				writeGatewayError(w, http.StatusGatewayTimeout, "compatibility_timeout", "The compatibility application did not answer in time")
			default:
				writeGatewayError(w, http.StatusBadGateway, "compatibility_unavailable", "The compatibility application failed to answer")
			}
		},
	}

	proxy.ServeHTTP(w, r.WithContext(ctx))
}

func (g *Gateway) applicationStatus(ctx context.Context, kind AppKind) (Status, error) {
	if g.states == nil {
		return Status{}, errors.New("compatgateway: no state provider")
	}
	return g.states.ApplicationStatus(ctx, kind)
}

func (g *Gateway) circuitOpen(kind AppKind) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.breakers[kind]
	if b == nil {
		return false
	}
	if b.openUntil.IsZero() {
		return false
	}
	if g.now().After(b.openUntil) {
		// Half-open: allow one attempt through; failure re-opens.
		b.openUntil = time.Time{}
		b.consecutiveFailures = g.failureThreshold - 1
		return false
	}
	return true
}

func (g *Gateway) recordFailure(kind AppKind) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.breakers[kind]
	if b == nil {
		b = &breaker{}
		g.breakers[kind] = b
	}
	b.consecutiveFailures++
	if b.consecutiveFailures >= g.failureThreshold {
		b.openUntil = g.now().Add(g.circuitCooldown)
	}
}

func (g *Gateway) recordSuccess(kind AppKind) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if b := g.breakers[kind]; b != nil {
		b.consecutiveFailures = 0
		b.openUntil = time.Time{}
	}
}

// signIdentity mints the signed internal request identity: the companion (and
// the private API behind it) can verify that a request entered through the
// gateway, which application family it was addressed to, and when.
func (g *Gateway) signIdentity(kind AppKind, traceID string) string {
	payload := fmt.Sprintf("v1:%s:%d:%s", kind, g.now().Unix(), traceID)
	mac := hmac.New(sha256.New, g.identitySecret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

// requestTraceID reuses the request ID minted by the router middleware when
// present, so gateway and native logs share one identifier.
func requestTraceID(r *http.Request) string {
	if id := chimw.GetReqID(r.Context()); id != "" {
		return id
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "trace-unavailable"
	}
	return hex.EncodeToString(buf[:])
}

// strippedPath removes the public prefix for applications that expect
// protocol-native paths: "/audiobookshelf/api/ping" becomes "/api/ping" and
// "/audiobookshelf" itself becomes "/". It drops the first segment rather
// than a literal prefix string, so it holds for whatever casing the client
// used on a case-insensitively matched family such as /emby.
func strippedPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	idx := strings.IndexByte(trimmed, '/')
	if idx < 0 {
		return "/"
	}
	return trimmed[idx:]
}

// rewriteRedirect keeps companion redirects on the canonical origin. A
// Location naming the private endpoint is rewritten to a same-origin path
// (with the public prefix restored for stripped-prefix applications); a
// Location naming any other host is refused.
func rewriteRedirect(resp *http.Response, route Route, endpoint *url.URL) error {
	location := resp.Header.Get("Location")
	if location == "" {
		return nil
	}
	parsed, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("compatgateway: unparseable redirect from %s", route.App)
	}
	if parsed.IsAbs() || parsed.Host != "" {
		if parsed.Host != endpoint.Host {
			return fmt.Errorf("compatgateway: %s redirected off-origin", route.App)
		}
		parsed.Scheme = ""
		parsed.Host = ""
	}
	// A backslash defeats the host check: "/\evil.example/phish" parses with
	// an empty host, but browsers normalize the backslash to "//" and leave
	// the origin. Refuse any Location whose path opens with a backslash,
	// bare or after the leading slash.
	if strings.HasPrefix(parsed.Path, `\`) || strings.HasPrefix(parsed.Path, `/\`) {
		return fmt.Errorf("compatgateway: %s redirect escapes the origin", route.App)
	}
	if route.StripPrefix && !strings.HasPrefix(parsed.Path, route.Prefix+"/") && parsed.Path != route.Prefix {
		parsed.Path = route.Prefix + parsed.Path
	}
	resp.Header.Set("Location", parsed.String())
	return nil
}

func writeUnavailable(w http.ResponseWriter, kind AppKind) {
	w.Header().Set("Retry-After", unavailableRetryAfter)
	writeGatewayError(w, http.StatusServiceUnavailable, "compatibility_unavailable",
		fmt.Sprintf("The %s compatibility application is not available", kind))
}

func writeGatewayError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{code, message})
}
