// Package outbound owns validation and transport policy for server-initiated
// requests to profile- or provider-controlled destinations.
package outbound

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// Policy describes the network destinations an outbound client may reach.
type Policy struct {
	AllowedSchemes map[string]struct{}
	AllowPrivate   bool
	MaxRedirects   int
	allowPrivate   func() bool
}

// ErrPrivateDestination identifies a destination rejected because at least
// one resolved address is private or special-use.
var ErrPrivateDestination = errors.New("outbound: destination is private or special-use")

// PublicHTTPPolicy permits public HTTP and HTTPS destinations and rejects
// private or special-use addresses. Callers that carry credentials should
// narrow AllowedSchemes to HTTPS only.
func PublicHTTPPolicy() Policy {
	return Policy{
		AllowedSchemes: map[string]struct{}{
			"http":  {},
			"https": {},
		},
		MaxRedirects: 10,
	}
}

// PublicHTTPSPolicy permits only public HTTPS destinations.
func PublicHTTPSPolicy() Policy {
	policy := PublicHTTPPolicy()
	policy.AllowedSchemes = map[string]struct{}{"https": {}}
	return policy
}

// WithPrivateAccess returns a policy whose private-destination exception is
// decided at request time. This is reserved for explicit administrator-owned
// development settings; profile input cannot provide the decision function.
func (p Policy) WithPrivateAccess(allowed func() bool) Policy {
	p.allowPrivate = allowed
	return p
}

func (p Policy) validateURL(target *url.URL) error {
	if target == nil {
		return fmt.Errorf("outbound: URL is required")
	}
	if _, ok := p.AllowedSchemes[strings.ToLower(target.Scheme)]; !ok {
		return fmt.Errorf("outbound: URL scheme %q is not allowed", target.Scheme)
	}
	if target.Hostname() == "" {
		return fmt.Errorf("outbound: URL host is required")
	}
	if target.User != nil {
		return fmt.Errorf("outbound: URL credentials are not allowed")
	}
	if target.Fragment != "" {
		return fmt.Errorf("outbound: URL fragments are not allowed")
	}
	if literal, err := netip.ParseAddr(target.Hostname()); err == nil && !p.addressAllowed(literal) {
		return ErrPrivateDestination
	}
	return nil
}

var deniedPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("fec0::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func (p Policy) addressAllowed(address netip.Addr) bool {
	if p.privateAllowed() {
		return address.IsValid()
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() {
		return false
	}
	for _, denied := range deniedPublicPrefixes {
		if denied.Contains(address) {
			return false
		}
	}
	return true
}

func (p Policy) privateAllowed() bool {
	return p.AllowPrivate || p.allowPrivate != nil && p.allowPrivate()
}
