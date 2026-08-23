package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Silo-Server/silo-server/internal/outbound"
)

// ValidateWebhookURL enforces the destination guardrails the profile cannot
// opt out of: HTTPS only, a well-formed host, and (unless the admin enabled
// private destinations for development) resolution to public addresses only.
// Returns the host for the denormalized url_host column.
func ValidateWebhookURL(rawURL string, allowPrivate bool) (host string, err error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("invalid URL")
	}
	if parsed.Scheme != schemeHTTPS {
		return "", fmt.Errorf("webhook URLs must use https")
	}
	host = parsed.Hostname()
	if host == "" {
		return "", fmt.Errorf("webhook URL has no host")
	}
	if len(host) > 253 {
		return "", fmt.Errorf("webhook URL host is too long")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("webhook URLs must not embed credentials")
	}
	policy := outbound.PublicHTTPSPolicy()
	policy.AllowPrivate = allowPrivate
	if err := outbound.NewClient(policy).Validate(context.Background(), parsed); err != nil {
		if errors.Is(err, outbound.ErrPrivateDestination) {
			return "", fmt.Errorf("webhook destinations on private or special-use networks are not allowed")
		}
		return "", fmt.Errorf("webhook host could not be resolved")
	}
	return host, nil
}

// discordWebhookURL matches Discord channel webhook endpoints for type
// auto-detection.
func discordWebhookURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	return strings.HasPrefix(parsed.Path, "/api/webhooks/")
}
