package apiv2

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Silo-Server/silo-server/internal/notifications"
)

// BloemUpgradePromptOutput is the installation's upgrade prompt.
type BloemUpgradePromptOutput struct{ Body notifications.UpgradePrompt }

// BloemUpgradePromptInput replaces the upgrade prompt.
type BloemUpgradePromptInput struct{ Body notifications.UpgradePrompt }

// registerBloemUpgradePromptDocument documents the alert a viewer gets when a
// stream is refused at their plan's limit (handlers.BloemUpgradePromptHandler).
func registerBloemUpgradePromptDocument(reg *Registry) {
	const auth = "Requires a Platform administrative context or an enabled administrator's unscoped API key; scoped keys and organization contexts are refused. "
	errors := map[int]string{
		401: "tenant_session_required or authorization_state_stale: the administrative context is missing, invalid or no longer current; unauthorized: the API key is invalid or its account is disabled.",
		403: "insufficient_platform_authority: the credential is neither a Platform context nor an administrator's unscoped API key, or the account is no longer an enabled administrator; forbidden: the API key's scopes exclude this route.",
		503: "upgrade_prompt_unavailable: the database is unavailable; tenant_unavailable or service_unavailable: the credential could not be checked.",
	}
	noStore := map[string]*huma.Header{"Cache-Control": {Schema: &huma.Schema{Type: "string", Enum: []any{"no-store"}}}}

	get := bloemChiDocumentOp(reg, "GET", "/admin/platform/upgrade-prompt", "getBloemUpgradePrompt", "engagement", "Read the upgrade prompt.", "bloemPlatformContext", http.StatusOK)
	get.Security = append(get.Security, map[string][]string{"bearerAuth": {}})
	get.Description = auth + "The prompt is the inbox alert, with a button to the storefront, that a viewer gets when a stream is refused because their plan's simultaneous-stream limit is reached. Off until set."
	get.Responses = map[string]*huma.Response{}
	bloemDocumentErrors[BloemNativeError](reg, &get, errors)
	get.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK), Headers: noStore}
	registerBloemChiDocument[struct{}, BloemUpgradePromptOutput](reg, get)

	put := bloemChiDocumentOp(reg, "PUT", "/admin/platform/upgrade-prompt", "putBloemUpgradePrompt", "engagement", "Replace the upgrade prompt.", "bloemPlatformContext", http.StatusOK)
	put.Security = append(put.Security, map[string][]string{"bearerAuth": {}})
	put.Description = auth + "Turning it on needs a title, a button label and an https link. A viewer gets at most one prompt per profile per UTC day, through the operational inbox and its push channels, unless their notifications are off."
	put.Responses = map[string]*huma.Response{}
	putErrors := map[int]string{
		400: "invalid_request: the body is not an upgrade prompt.",
		422: "invalid_upgrade_prompt: a field is too long, the link is not https, or a prompt turned on lacks its title, button label or link.",
	}
	for code, text := range errors {
		putErrors[code] = text
	}
	bloemDocumentErrors[BloemNativeError](reg, &put, putErrors)
	put.Responses["200"] = &huma.Response{Description: http.StatusText(http.StatusOK), Headers: noStore}
	registerBloemChiDocument[BloemUpgradePromptInput, BloemUpgradePromptOutput](reg, put)
}
