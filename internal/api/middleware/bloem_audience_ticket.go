package middleware

// Bloem-owned. The handshake principal for a consumed audience ticket.
//
// The ticket authenticates as the credential that minted it, not as a weaker
// generic principal: the original auth method, tenant, device, impersonator,
// and API key bindings are restored (see auth.NewAudienceTicket), so every
// downstream guard sees the same credential it saw at mint time.
//
// PIN verification is skipped only because the minting request already
// verified the profile the ticket names. That makes the ticket's profile the
// only profile this request may act as: a caller-supplied X-Profile-Id is
// replaced, and removed when the ticket names none, so a header cannot select
// a PIN-protected profile the ticket never verified.

import (
	"context"
	"net/http"

	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
)

// LoginSessions returns the validator this middleware checks login sessions
// with, so a credential derived from a login session (an administrative
// context) is revoked by exactly the same check. Nil when unwired.
func (am *AuthMiddleware) LoginSessions() SessionValidator {
	if am == nil {
		return nil
	}
	return am.sessionValidator
}

type audienceTicketAuthorizedKey struct{}

func withAudienceTicketAuthorized(ctx context.Context) context.Context {
	return context.WithValue(ctx, audienceTicketAuthorizedKey{}, true)
}

// IsAudienceTicketAuthorized reports whether a consumed audience ticket
// authenticated this request.
func IsAudienceTicketAuthorized(ctx context.Context) bool {
	authorized, _ := ctx.Value(audienceTicketAuthorizedKey{}).(bool)
	return authorized
}

// audienceTicketPrincipal revalidates the ticket's credential binding and
// returns its claims. ok is false when a response has been written.
func audienceTicketPrincipal(w http.ResponseWriter, r *http.Request, am *AuthMiddleware, principal auth.AudienceTicket) (*auth.Claims, bool) {
	claims := principal.Claims()
	if claims.TokenType == auth.TokenTypeAPIKey {
		// An API key has no login session. Check both its captured and fresh
		// authority: a ticket must not outlive key revocation or widen scopes.
		if !apiKeyScopesAllow(claims.APIKeyScopes, r) {
			writeForbidden(w, "API key scopes do not permit this route")
			return nil, false
		}
		loader, ok := am.apiKeyValidator.(interface {
			GetByID(context.Context, int64) (*models.APIKey, error)
		})
		if !ok || am.apiKeyUserLoader == nil || claims.APIKeyID <= 0 {
			writeUnauthorized(w, "Invalid API key", ReasonInvalidCredential)
			return nil, false
		}
		key, err := loader.GetByID(r.Context(), claims.APIKeyID)
		if err != nil || key == nil || key.ID != claims.APIKeyID || key.UserID != claims.UserID {
			writeUnauthorized(w, "Invalid API key", ReasonInvalidCredential)
			return nil, false
		}
		user, err := am.apiKeyUserLoader.GetByID(r.Context(), key.UserID)
		if err != nil || user == nil || !user.Enabled || user.ID != claims.UserID || user.Role != claims.Role || user.AccountIncarnationID.String() != claims.AccountIncarnationID {
			writeUnauthorized(w, "API key owner is no longer valid", ReasonInvalidCredential)
			return nil, false
		}
		if !apiKeyScopesAllow(key.Scopes, r) {
			writeForbidden(w, "API key scopes do not permit this route")
			return nil, false
		}
		claims.RateTier = key.RateTier
	} else {
		// Every other ticket was minted from a login session and lives only
		// as long as that session does.
		if claims.SessionID == "" || am.sessionValidator == nil {
			writeUnauthorized(w, "Session is no longer valid", "session_expired")
			return nil, false
		}
		valid, err := am.checkSession(r.Context(), claims.SessionID)
		if err != nil || !valid {
			writeUnauthorized(w, "Session is no longer valid", "session_expired")
			return nil, false
		}
	}
	if claims.ProfileID != "" {
		r.Header.Set("X-Profile-Id", claims.ProfileID)
	} else {
		r.Header.Del("X-Profile-Id")
	}
	return claims, true
}
