package auth

import (
	"context"
	"net"
	"strings"
)

// AuthMethodPreviewGuest is a guest session on the shared preview account,
// opened only by exchanging a Garden-signed preview ticket.
const AuthMethodPreviewGuest = "preview_guest"

// PreviewGuestSettings is the part of the preview configuration that decides
// whether a guest session may still be used.
type PreviewGuestSettings struct {
	Enabled   bool
	Host      string
	AccountID int
}

// PreviewGuestSettingsSource reads the current preview configuration.
type PreviewGuestSettingsSource interface {
	PreviewGuestSettings(ctx context.Context) (PreviewGuestSettings, error)
}

type requestHostKey struct{}

// WithRequestHost records the request's Host header (never X-Forwarded-Host)
// for code that has only a context, such as refresh.
func WithRequestHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, requestHostKey{}, host)
}

// RequestHostFromContext returns the host WithRequestHost recorded, or "".
func RequestHostFromContext(ctx context.Context) string {
	host, _ := ctx.Value(requestHostKey{}).(string)
	return host
}

// HostOnly normalizes a Host header value: lower case, no port, no trailing dot.
func HostOnly(hostport string) string {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.Trim(host, "[]"), ".")
}
