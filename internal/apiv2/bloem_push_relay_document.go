package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// BloemPushRelayRegisterOutput is the credential Bloem's push relay issued.
type BloemPushRelayRegisterOutput struct {
	Body handlers.BloemPushRelayRegistration
}

// registerBloemPushRelayDocument documents the server's registration with
// Bloem's push relay (handlers.BloemPushRelayHandler), the counterpart of
// Silo's registerAdminNotificationRelay for Silo's relay.
func registerBloemPushRelayDocument(reg *Registry) {
	register := bloemStorefrontOp(reg, "POST", "/notifications/push/relay/register", "registerBloemPlatformPushRelay",
		"Register the server with Bloem's push relay again, or rotate its credential there.", http.StatusOK, map[int]string{
			422: "relay_deployment_rejected: Bloem's relay refused to register this server.",
			429: "relay_rate_limited: Bloem's relay is limiting registrations; Retry-After says when to try again.",
			502: "relay_error or the relay's own code: Bloem's relay failed or could not be reached.",
			503: "push_relay_unavailable: this server has no push backing. relay_unavailable: Bloem's relay is unavailable. tenant_unavailable or service_unavailable: the credential or the account could not be checked.",
		})
	register.Description += "Takes no body. Bloem's push relay delivers the Bloem Admin app's notifications; " +
		"Silo's apps go through Silo's relay, which Silo's push settings register (registerAdminNotificationRelay). " +
		"A working credential is rotated, and a relay that no longer accepts it gets a fresh registration. " +
		"A missing credential, or one parked because the relay disabled this deployment, is replaced by a fresh registration; " +
		"that is the only way to resume the Bloem Admin app's push after the relay disabled the deployment. " +
		"The result is stored under bloem.notifications.push_relay_*; Silo's relay credential is never read or changed. " +
		"The response never carries the credential."
	register.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK),
		Headers: map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}}
	registerBloemChiDocument[struct{}, BloemPushRelayRegisterOutput](reg, register)
}
