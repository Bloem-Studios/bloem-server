package handlers

import (
	"context"
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/lifecycleidempotency"
	"github.com/Silo-Server/silo-server/internal/settingscontract"
)

// SetLifecycleIdempotency installs receipt-first coordination for admin
// account setting mutations.
func (h *SettingValuesHandler) SetLifecycleIdempotency(coordinator lifecycleidempotency.Coordinator, digester lifecycleidempotency.RequestDigester) {
	h.lifecycle = coordinator
	h.lifecycleDigest = digester
}

// effectiveSettingValuesResponse names the existing effective-values envelope
// so the client DTO generator can publish its exact wire shape.
type effectiveSettingValuesResponse struct {
	Settings []effectiveSettingValueResponse `json:"settings"`
	Revision int                             `json:"revision"`
}

// bloemSettingValuesHandlerExt holds the Bloem-only SettingValuesHandler
// dependencies for lifecycle receipts on admin setting mutations.
type bloemSettingValuesHandlerExt struct {
	lifecycle       lifecycleidempotency.Coordinator
	lifecycleDigest lifecycleidempotency.RequestDigester
}

// bloemRejectDirectProfileAccountScope refuses account-scoped setting access
// from a direct profile session.
func bloemRejectDirectProfileAccountScope(ctx context.Context, req SettingIdentityRequest) *APIError {
	if settingscontract.Scope(strings.TrimSpace(req.Scope)) == settingscontract.ScopeAccount && apimw.IsDirectProfileSession((&http.Request{}).WithContext(ctx)) {
		return apiError(403, "forbidden", "Direct profile sessions cannot use account-scoped settings")
	}
	return nil
}
