package apiv2

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/api/handlers"
)

// The organization live ticket route and socket: change signals for the
// organization pages' streams, people and activity lists. Handlers are in
// internal/api/handlers/bloem_org_live.go; the contract is
// docs/design/bloem-org-live.md.

const (
	bloemOrganizationLiveTicketScheme = "bloemOrganizationLiveTicket"

	bloemOrganizationLiveAuthority = "Organization-scoped administrative context, as for the organization's other routes. A platform-scoped context is refused (403 insufficient_organization_authority). A context opened under break-glass support access is accepted. The context is revalidated on every request here and, for the life of the socket, every 15 seconds."
	bloemOrganizationLiveFeedDown  = "live_unavailable (Retry-After: 5): the server's change feed is down. Back off; after 3 failed attempts in a row fall back to polling."
)

func registerBloemOrganizationLiveDocument(reg *Registry) {
	reg.api.OpenAPI().Components.SecuritySchemes[bloemOrganizationLiveTicketScheme] = &huma.SecurityScheme{
		Type: "apiKey", In: "header", Name: "Sec-WebSocket-Protocol",
		Description: "A single-use ticket from createBloemOrganizationLiveTicket, offered as the second subprotocol: Sec-WebSocket-Protocol: bloem-live.v1, ticket.<ticket>. Valid for 30 seconds. It is never accepted in a URL.",
	}
	{
		op := bloemAdminDocumentOp(reg, "POST", "/admin/organization/live/ticket", "createBloemOrganizationLiveTicket", "Mint a single-use ticket for the organization live socket.", "bloemOrganizationContext", 200)
		op.Description = bloemOrganizationLiveAuthority + " The ticket binds the socket to the one organization the context names and carries the context's claims, never the context itself. It is single use and valid for 30 seconds; fetch a new one for every connection. The route takes no body (an empty body, {} or null). Mint at most 30 a minute per account. The response is Cache-Control: no-store. Advertised publicly by organization_live_v1 in feature_tokens."
		bloemDocumentErrors[BloemNativeError](reg, &op, map[int]string{
			400: "invalid_request: the route takes no body.",
			429: "rate_limited: more than 30 tickets a minute for the account on this server (Retry-After: seconds).",
		})
		bloemDocumentDescribe(&op, map[int]string{
			503: "tenant_unavailable: administrative authorization is unavailable. " + bloemOrganizationLiveFeedDown,
		})
		op.Responses["429"].Headers = map[string]*huma.Header{
			"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds to wait before retrying."},
		}
		op.Responses["503"].Headers = map[string]*huma.Header{
			"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds to wait before retrying, when the change feed is down."},
		}
		registerBloemChiDocument[struct{}, bloemJSONOutput[handlers.OrgLiveTicket]](reg, op)
	}
	{
		op := bloemChiDocumentOp(reg, "GET", "/organization/live/ws", "connectBloemOrganizationLiveSocket", "realtime", "Open the organization live socket with a ticket.", bloemOrganizationLiveTicketScheme, 101)
		op.Description = "WebSocket upgrade, protocol bloem-live.v1. The path is outside /admin because a WebSocket client cannot send an Authorization header; the ticket in Sec-WebSocket-Protocol is the credential, and the context it carries is revalidated as the ticket route does. " +
			"Client frames (JSON text, at most 4 KiB): {\"op\":\"subscribe\",\"channels\":[...],\"resume\":null} and {\"op\":\"unsubscribe\",\"channels\":[...]}. Channels: org.streams, org.people, org.activity. " +
			"Server frames: {\"op\":\"change\",\"seq\",\"channel\",\"id\",\"rev\",\"kind\"} with kind created, updated or deleted; {\"op\":\"heartbeat\",\"seq\"} every 25 seconds; {\"op\":\"resync\",\"seq\",\"channel\"}, sent after every subscribe and whenever signals may have been lost; {\"op\":\"error\",\"code\",\"channel\"} with code unknown_channel, unavailable or too_many_channels. " +
			"A change names an item and a revision, never its content: re-read it through the matching REST route. " +
			"Close codes: 4401 invalid ticket, 4403 the context no longer holds, 4408 the context or the one-hour limit ended (renew and reconnect), 4429 more than 5 sockets for the account and organization on this server, 1012 server restart, 1013 try again later, 1008 a frame this protocol does not accept, 1009 a frame over 4 KiB. " +
			"Full contract: docs/design/bloem-org-live.md."
		op.Parameters = []*huma.Param{
			{Name: "Sec-WebSocket-Protocol", In: "header", Required: true, Schema: &huma.Schema{Type: "string"}, Description: "bloem-live.v1, ticket.<single-use-ticket>."},
			{Name: "Origin", In: "header", Schema: &huma.Schema{Type: "string"}, Description: "Absent, an app scheme or null, or a browser origin that matches the server's public origin."},
		}
		op.Responses = map[string]*huma.Response{
			"101": {Description: "Switching protocols. The ticket is checked after the upgrade: a bad, used, expired or foreign ticket closes the socket with 4401."},
			"400": {Description: "Plain text: not a valid handshake (a ticket or token in the URL, a body, a missing or wrong subprotocol, a malformed upgrade).", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}},
			"403": {Description: "Plain text: the browser origin is not allowed.", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}},
			"503": {
				Description: "Plain text: live updates are unavailable on this server, or its change feed is down (Retry-After: 5).",
				Content:     map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}},
				Headers:     map[string]*huma.Header{"Retry-After": {Schema: &huma.Schema{Type: "integer"}, Description: "Seconds to wait before retrying, when the change feed is down."}},
			},
		}
		registerBloemChiDocument[struct{}, struct{}](reg, op)
	}
}
