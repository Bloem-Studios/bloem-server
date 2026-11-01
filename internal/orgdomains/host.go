// Package orgdomains serves each organization on its own domains, under its own
// branding. A registered host maps a request to an organization for
// presentation only: which branding it shows and which address its links use.
// It never decides authorization. See docs/architecture/bloem-organization-domains.md.
package orgdomains

import (
	"errors"
	"net"
	"regexp"
	"strings"
)

// ErrInvalidHost reports a host that cannot be registered.
var ErrInvalidHost = errors.New("orgdomains: a host is a lower-case name with at least one dot, without scheme, port or path")

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// NormalizeHost is raw as a host to register: lower case, at least one dot,
// no scheme, port or path. The same rule is the table's check constraint.
func NormalizeHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if len(host) > 253 || !hostPattern.MatchString(host) {
		return "", ErrInvalidHost
	}
	return host, nil
}

// requestHostKey is the host a request addressed, as a lookup key: lower
// case, without a port or IPv6 brackets.
func requestHostKey(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	} else {
		host = strings.Trim(host, "[]")
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
