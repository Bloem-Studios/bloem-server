package outbound

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

type resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type dialContext func(ctx context.Context, network, address string) (net.Conn, error)

type clientOptions struct {
	resolver resolver
	dial     dialContext
	timeout  time.Duration
}

// Option configures a Client without weakening its destination policy.
type Option func(*clientOptions)

// WithResolver supplies the resolver used for destination validation. It is
// primarily useful for deterministic tests and controlled resolver stacks.
func WithResolver(value resolver) Option {
	return func(options *clientOptions) {
		if value != nil {
			options.resolver = value
		}
	}
}

// WithTimeout sets the total request timeout. Zero leaves the standard
// timeout in place.
func WithTimeout(value time.Duration) Option {
	return func(options *clientOptions) {
		if value > 0 {
			options.timeout = value
		}
	}
}

// Client couples URL validation, DNS resolution, and dialing. The transport
// dials only an address from the exact answer set it validated.
type Client struct {
	policy   Policy
	resolver resolver
	http     *http.Client
}

// NewClient constructs an outbound client for one immutable policy.
func NewClient(policy Policy, options ...Option) *Client {
	configuration := clientOptions{
		resolver: net.DefaultResolver,
		timeout:  30 * time.Second,
	}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second}
	configuration.dial = baseDialer.DialContext
	for _, option := range options {
		option(&configuration)
	}

	client := &Client{policy: policy, resolver: configuration.resolver}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		DialContext:           client.validatedDial(configuration.resolver, configuration.dial),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: configuration.timeout,
	}
	client.http = &http.Client{
		Timeout:   configuration.timeout,
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if policy.MaxRedirects >= 0 && len(via) > policy.MaxRedirects {
				return fmt.Errorf("outbound: too many redirects")
			}
			return policy.validateURL(request.URL)
		},
	}
	return client
}

// HTTPClient exposes the guarded standard-library client for callers that
// need streaming request/response handling.
func (c *Client) HTTPClient() *http.Client {
	return c.http
}

// Do validates the initial URL before the standard client handles it. Redirect
// URLs are validated independently by CheckRedirect.
func (c *Client) Do(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, fmt.Errorf("outbound: request is required")
	}
	if err := c.policy.validateURL(request.URL); err != nil {
		return nil, err
	}
	return c.http.Do(request)
}

// Validate checks a URL and its complete current DNS answer set without
// issuing a request. Delivery still repeats validation in the dialer so this
// method is suitable for registration-time feedback, not authorization.
func (c *Client) Validate(ctx context.Context, target *url.URL) error {
	if err := c.policy.validateURL(target); err != nil {
		return err
	}
	if c.policy.privateAllowed() {
		return nil
	}
	addresses, err := resolve(ctx, c.resolver, target.Hostname())
	if err != nil {
		return err
	}
	if len(addresses) == 0 {
		return fmt.Errorf("outbound: destination has no addresses")
	}
	for _, address := range addresses {
		if !c.policy.addressAllowed(address) {
			return ErrPrivateDestination
		}
	}
	return nil
}

func (c *Client) validatedDial(resolver resolver, dial dialContext) dialContext {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("outbound: split destination: %w", err)
		}
		addresses, err := resolve(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("outbound: destination has no addresses")
		}
		for _, candidate := range addresses {
			if !c.policy.addressAllowed(candidate) {
				return nil, ErrPrivateDestination
			}
		}
		selected := addresses[0].Unmap()
		return dial(ctx, network, net.JoinHostPort(selected.String(), port))
	}
}

func resolve(ctx context.Context, resolver resolver, host string) ([]netip.Addr, error) {
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal}, nil
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("outbound: resolve %q: %w", host, err)
	}
	return addresses, nil
}
