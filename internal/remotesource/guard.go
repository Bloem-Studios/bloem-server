package remotesource

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxRedirects bounds a redirect chain; every hop is checked like the first.
const MaxRedirects = 10

// Resolver looks up a host name. *net.Resolver implements it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options configures a Guard.
type Options struct {
	// AllowPrivate reports the platform setting that lets addresses on the
	// server's private network through. Nil means never.
	AllowPrivate func(context.Context) bool
	// Resolver defaults to net.DefaultResolver.
	Resolver Resolver
	// DialTimeout, TLSHandshakeTimeout and ResponseHeaderTimeout default to
	// 10, 10 and 20 seconds.
	DialTimeout, TLSHandshakeTimeout, ResponseHeaderTimeout time.Duration
}

// Guard is the only way Bloem opens a connection for a `.strm` URL. It
// resolves each host itself, refuses every blocked address before dialing,
// dials the checked IP literal, and checks the socket address again in the
// dialer's Control hook. Redirects go through the same dialer, so a redirect
// to a private address or a name that rebinds to one is refused at its own
// connection.
type Guard struct {
	allowPrivate func(context.Context) bool
	resolver     Resolver
	dialer       *net.Dialer
	transport    *http.Transport
	client       *http.Client

	// public, when set, treats an address as public. Tests only: they run
	// their servers on loopback.
	public func(netip.Addr) bool

	mu        sync.Mutex
	lastAllow bool
}

// NewGuard builds a Guard with its own connection pool. Environment proxies
// are ignored: a proxy would hide the address actually reached.
func NewGuard(opts Options) *Guard {
	g := &Guard{allowPrivate: opts.AllowPrivate, resolver: opts.Resolver}
	if g.resolver == nil {
		g.resolver = net.DefaultResolver
	}
	dialTimeout := orDefault(opts.DialTimeout, 10*time.Second)
	g.dialer = &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second, ControlContext: g.control}
	g.transport = &http.Transport{
		Proxy:                 nil,
		DialContext:           g.dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   orDefault(opts.TLSHandshakeTimeout, 10*time.Second),
		ResponseHeaderTimeout: orDefault(opts.ResponseHeaderTimeout, 20*time.Second),
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       60 * time.Second,
		DisableCompression:    true,
	}
	g.client = &http.Client{Transport: g.transport, CheckRedirect: checkRedirect}
	return g
}

func orDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// Client returns the guarded client. It has no overall timeout: callers bound
// each request with its context, because a stream body can run for hours.
func (g *Guard) Client() *http.Client { return g.client }

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return fmt.Errorf("remote source: more than %d redirects", MaxRedirects)
	}
	switch strings.ToLower(req.URL.Scheme) {
	case "http", "https":
		return nil
	default:
		return ErrBlockedAddress
	}
}

// allowed reads the setting once per connection. A switch from allowed to
// refused drops idle connections, which could lead to private addresses.
func (g *Guard) allowed(ctx context.Context) bool {
	allow := g.allowPrivate != nil && g.allowPrivate(ctx)
	g.mu.Lock()
	dropIdle := g.lastAllow && !allow
	g.lastAllow = allow
	g.mu.Unlock()
	if dropIdle {
		g.transport.CloseIdleConnections()
	}
	return allow
}

func (g *Guard) blocked(addr netip.Addr) bool {
	if g.public != nil && g.public(addr) {
		return false
	}
	return BlockedAddr(addr)
}

// Check resolves a URL's host and refuses it when the scheme is not http or
// https, or when private addresses are not allowed and every address the host
// resolves to is blocked. It is an early, readable refusal for scans; the
// dialer repeats the address check for every connection.
func (g *Guard) Check(ctx context.Context, rawURL string) error {
	if err := ValidateURL(rawURL); err != nil {
		return err
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrInvalidStrm
	}
	if g.allowed(ctx) {
		return nil
	}
	_, err = g.resolveAllowed(ctx, u.Hostname())
	return err
}

func (g *Guard) resolveAllowed(ctx context.Context, host string) ([]netip.Addr, error) {
	if BlockedName(host) {
		return nil, ErrBlockedAddress
	}
	addrs, err := g.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	out := addrs[:0]
	for _, addr := range addrs {
		if !g.blocked(addr) {
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, ErrBlockedAddress
	}
	return out, nil
}

func (g *Guard) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr.WithZone("").Unmap()}, nil
	}
	addrs, err := g.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("remote source: resolve host: %w", err)
	}
	if len(addrs) == 0 {
		return nil, errors.New("remote source: host has no address")
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, addr.WithZone("").Unmap())
	}
	return out, nil
}

// dial resolves the host itself and connects only to checked IP literals, so
// the address checked is the address dialed.
func (g *Guard) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	if g.allowed(ctx) {
		addrs, err = g.resolve(ctx, host)
	} else {
		addrs, err = g.resolveAllowed(ctx, host)
	}
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, addr := range addrs {
		conn, err := g.dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// control runs on the socket about to connect: the last word on the address.
func (g *Guard) control(ctx context.Context, _, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return ErrBlockedAddress
	}
	if !g.allowed(ctx) && g.blocked(addrPort.Addr().WithZone("").Unmap()) {
		return ErrBlockedAddress
	}
	return nil
}

// BlockedName reports host names that always mean the server's own network.
func BlockedName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	switch host {
	case "localhost", "metadata", "metadata.google.internal", "instance-data", "instance-data.ec2.internal":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT, including Alibaba's metadata address
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, and the broadcast address
	netip.MustParsePrefix("fec0::/10"),     // deprecated site-local
	netip.MustParsePrefix("100::/64"),      // discard-only
}

var (
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
	sixTo = netip.MustParsePrefix("2002::/16")
)

// BlockedAddr reports whether an address is on the server's private network:
// unspecified, loopback, link-local (169.254.169.254 included), private
// (RFC 1918 and fc00::/7), CGNAT, multicast, reserved, and IPv4 addresses
// embedded in IPv4-mapped, NAT64 or 6to4 IPv6 addresses.
func BlockedAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		raw := addr.As16()
		switch {
		case nat64.Contains(addr):
			return BlockedAddr(netip.AddrFrom4([4]byte{raw[12], raw[13], raw[14], raw[15]}))
		case sixTo.Contains(addr):
			return BlockedAddr(netip.AddrFrom4([4]byte{raw[2], raw[3], raw[4], raw[5]}))
		}
	}
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, prefix := range blockedPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
