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
	// States resolves application lifecycle state. Required for any route
	// family not served by a LocalHandler.
	States StateProvider
	// LocalHandlers dispatches a route family straight to an in-process
	// http.Handler instead of proxying to an enrolled companion's Endpoint.
	// It exists for first-party services that already run in this process
	// (the embedded Jellyfin-compatibility server, the Audiobookshelf
	// handler) and have no separate trust/enrollment lifecycle: there is no
	// Endpoint to negotiate, so the States/Routable machinery does not apply
	// to them at all. A route family with no entry here keeps the existing
	// enrolled-companion proxy behavior, States included, unchanged.
	LocalHandlers map[AppKind]http.Handler
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
	localHandlers    map[AppKind]http.Handler
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
	var localHandlers map[AppKind]http.Handler
	if len(cfg.LocalHandlers) > 0 {
		localHandlers = make(map[AppKind]http.Handler, len(cfg.LocalHandlers))
		for kind, handler := range cfg.LocalHandlers {
			if handler == nil {
				continue
			}
			localHandlers[kind] = handler
		}
	}
	gateway := &Gateway{
		states:           cfg.States,
		localHandlers:    localHandlers,
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

	// A dot segment never belongs in a protocol path, and percent-encoding
	// hides it from the mux's own path cleaning: "%2e%2e%2f%2e%2e" decodes to
	// "../.." only once URL.Path is read. Refuse rather than forward — the
	// whole path is checked, since stripping only removes a leading segment
	// that had to match a family name. This applies to both dispatch paths
	// below: a local, in-process handler deserves the same hardening a
	// proxied one gets.
	if hasDotSegment(r.URL.Path) || hasDotSegment(r.URL.EscapedPath()) {
		writeGatewayError(w, http.StatusBadRequest, "invalid_path", "The request path is not a valid compatibility path")
		return
	}

	// Stripping happens twice — once on the decoded path, once on the escaped
	// one — and the two must agree, or net/url discards RawPath and the
	// encoding the client sent is lost. They disagree when the family segment
	// itself carries an encoded slash ("/emby%2fItems/x"), which no real
	// client sends.
	if route.StripPrefix && !stripsConsistently(r.URL) {
		writeGatewayError(w, http.StatusBadRequest, "invalid_path", "The request path is not a valid compatibility path")
		return
	}

	if r.ContentLength > g.maxRequestBytes {
		writeGatewayError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request body exceeds the compatibility gateway limit")
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, g.maxRequestBytes)
	}

	// First-party, in-process services (the embedded Jellyfin-compatibility
	// server, the Audiobookshelf handler) are dispatched directly here,
	// ahead of any States lookup: they have no Endpoint, no enrollment
	// record, and no health/circuit lifecycle to consult, so the
	// States-based proxy path below never runs for them. A route family
	// with no registered local handler falls through unchanged to that
	// path, so an unenrolled/unregistered companion still answers
	// compatibility_unavailable exactly as before.
	if handler, ok := g.localHandlers[route.App]; ok && handler != nil {
		g.serveLocal(w, r, route, handler)
		return
	}

	status, err := g.applicationStatus(r.Context(), route.App)
	if err != nil || !status.Routable() {
		writeUnavailable(w, route.App)
		return
	}

	if g.circuitOpen(route.App) {
		writeUnavailable(w, route.App)
		return
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
				// Strip the decoded and the escaped path by the same rule and
				// keep them in step. Rewriting Path alone and clearing
				// RawPath re-encodes the request line from the decoded form,
				// so an encoded slash ("a%2Fb") would reach the companion as
				// a real separator — a path its client never sent.
				out.URL.RawPath = strippedPath(out.URL.EscapedPath())
				out.URL.Path = strippedPath(out.URL.Path)
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

// serveLocal dispatches a request the fixed table owns straight to a
// first-party in-process handler, applying the same prefix-stripping rule
// the proxy path applies for a StripPrefix route so the handler sees the
// same path shape either way. jellycompat's own router re-derives the
// original path from context for the families that need it, so the request
// otherwise reaches the handler unmodified — no signed identity header, no
// X-Forwarded-* rewrite, no circuit breaker: those all exist for the
// network hop to a companion process, which this dispatch never makes.
func (g *Gateway) serveLocal(w http.ResponseWriter, r *http.Request, route Route, handler http.Handler) {
	if !route.StripPrefix {
		handler.ServeHTTP(w, r)
		return
	}
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.RawPath = strippedPath(r.URL.EscapedPath())
	urlCopy.Path = strippedPath(r.URL.Path)
	clone.URL = &urlCopy
	handler.ServeHTTP(w, clone)
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

// hasDotSegment reports whether any path segment is a dot segment, seeing
// through the two disguises a companion might undo for itself: a path
// parameter after a semicolon, and one further round of percent-decoding.
// Neither Go nor Node strips ;-params or double-decodes, so this is
// hardening rather than a live hole — but the check costs nothing and the
// gateway should not be the component that forwards one.
func hasDotSegment(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			continue
		}
		if isDotSegment(segment) {
			return true
		}
		if decoded, err := url.PathUnescape(segment); err == nil && decoded != segment && isDotSegment(decoded) {
			return true
		}
	}
	return false
}

func isDotSegment(segment string) bool {
	if parameter := strings.IndexByte(segment, ';'); parameter >= 0 {
		segment = segment[:parameter]
	}
	return segment == "." || segment == ".."
}

// stripsConsistently reports whether stripping the decoded and the escaped
// path yields the same path, so RawPath survives on the outgoing request.
func stripsConsistently(u *url.URL) bool {
	decoded, err := url.PathUnescape(strippedPath(u.EscapedPath()))
	return err == nil && decoded == strippedPath(u.Path)
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
