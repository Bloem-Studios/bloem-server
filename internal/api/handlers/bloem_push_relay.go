package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// BloemPushRelayRegistration is the credential Bloem's push relay issued,
// without the credential itself: the shape of Silo's relay registration
// (apiv2.NotificationRelayRegistration).
type BloemPushRelayRegistration struct {
	RelayURL         string    `json:"relay_url" doc:"Bloem's push relay origin."`
	DeploymentID     string    `json:"deployment_id" doc:"This server's deployment at Bloem's relay."`
	KeyPrefix        string    `json:"key_prefix" doc:"The stored capability's public prefix."`
	APIKeyConfigured bool      `json:"api_key_configured" doc:"Always true: a credential is stored."`
	RelayRequestID   string    `json:"relay_request_id,omitempty" doc:"The relay's request id, for its support."`
	APNsTopics       []string  `json:"apns_topics,omitempty" doc:"The Apple topics the relay sends for, when it says."`
	ExpiresAt        time.Time `json:"expires_at" doc:"When the capability expires; the server renews it before then."`
}

type bloemPushRelayRegistrar interface {
	RegisterBloemRelay(context.Context) (notifications.RelayCredentialResult, error)
}

// BloemPushRelayHandler registers this server with Bloem's push relay again,
// for the Bloem Admin app's push. Silo's push settings manage Silo's relay
// only (docs/architecture/bloem-push-relays.md).
type BloemPushRelayHandler struct{ relay bloemPushRelayRegistrar }

// NewBloemPushRelayHandler is nil when the server has no admin push service.
func NewBloemPushRelayHandler(push *notifications.AdminPushService) *BloemPushRelayHandler {
	if push == nil {
		return nil
	}
	return &BloemPushRelayHandler{relay: push}
}

// NewBloemPushRelayHandlerWith serves relay, for tests.
func NewBloemPushRelayHandlerWith(relay bloemPushRelayRegistrar) *BloemPushRelayHandler {
	return &BloemPushRelayHandler{relay: relay}
}

// HandleRegister handles POST
// /api/bloem/v2/admin/platform/notifications/push/relay/register. It takes no
// body: the relay is the server's own Bloem relay, never one the caller names.
// Relay failures answer as Silo's register route does.
func (h *BloemPushRelayHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	result, err := h.relay.RegisterBloemRelay(r.Context())
	if errors.Is(err, notifications.ErrBloemRelayUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "push_relay_unavailable", "Bloem's push relay is not available on this server")
		return
	}
	if err != nil {
		status, code, message := mapRelayRegistrationError(err)
		if relayErr, ok := errors.AsType[notifications.RelayCredentialError](err); ok && relayErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(relayErr.RetryAfter.Seconds())))))
		}
		writeError(w, status, code, message)
		return
	}
	c := result.Credential
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, BloemPushRelayRegistration{
		RelayURL: c.RelayURL, DeploymentID: c.DeploymentID, KeyPrefix: c.KeyPrefix, APIKeyConfigured: true,
		RelayRequestID: result.RequestID, APNsTopics: result.APNsTopics, ExpiresAt: c.ExpiresAt.UTC(),
	})
}
