package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Silo-Server/silo-server/internal/outbound"
)

// Bloem routes webhook destination checks and delivery clients through the
// shared outbound package (DNS-answer validation, pinned dial, per-redirect
// validation) instead of Silo's in-package deny list and dialer hook. The
// Silo implementations in webhook_guard.go / webhook_http.go stay untouched;
// the hooks below take over when set.

// webhookDestinationGuard replaces the private-network / resolution half of
// ValidateWebhookURL. It runs after the scheme/host/credential checks.
var webhookDestinationGuard = validateWebhookDestinationOutbound

// notificationHTTPClientFactory replaces newNotificationHTTPClient's body.
var notificationHTTPClientFactory = newOutboundNotificationHTTPClient

func init() {
	// The outbound transport reports refused destinations with its own
	// sentinel; classifyWebhookError matches errPrivateDestination.
	errPrivateDestination = outbound.ErrPrivateDestination
}

func validateWebhookDestinationOutbound(parsed *url.URL, host string, allowPrivate bool) (string, error) {
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

func newOutboundNotificationHTTPClient(allowPrivate func() bool, requestTimeout time.Duration) *http.Client {
	policy := outbound.PublicHTTPSPolicy().WithPrivateAccess(allowPrivate)
	policy.MaxRedirects = webhookMaxRedirects
	return outbound.NewClient(policy, outbound.WithTimeout(requestTimeout)).HTTPClient()
}
