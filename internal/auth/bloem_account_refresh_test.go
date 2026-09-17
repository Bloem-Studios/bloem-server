package auth

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

func TestAccountRefreshBindingHolds(t *testing.T) {
	incarnation := uuid.MustParse("11111111-2222-4333-8444-555555555555")
	session := &models.AuthSession{ID: "login-session", UserID: 7}
	user := &models.User{ID: 7, AccountIncarnationID: incarnation}
	issued := Claims{UserID: 7, AccountIncarnationID: incarnation.String(), Role: "user", SessionID: "login-session", TokenType: TokenTypeRefresh}

	if !accountRefreshBindingHolds(&issued, session, user) {
		t.Fatal("an account refresh token as issued was refused")
	}
	legacy := issued
	legacy.AccountIncarnationID = ""
	if !accountRefreshBindingHolds(&legacy, session, user) {
		t.Fatal("a refresh token issued before incarnations was refused")
	}

	for name, tamper := range map[string]func(*Claims){
		"other account":         func(c *Claims) { c.UserID = 8 },
		"replaced incarnation":  func(c *Claims) { c.AccountIncarnationID = uuid.NewString() },
		"stale organization":    func(c *Claims) { c.OrganizationID = uuid.NewString() },
		"stale membership":      func(c *Claims) { c.MembershipID = uuid.NewString() },
		"stale policy revision": func(c *Claims) { c.PolicyRevision = 3 },
		"stale security rev":    func(c *Claims) { c.SecurityRevision = 3 },
		"credential revision":   func(c *Claims) { c.CredentialRevision = 2 },
		"direct profile method": func(c *Claims) { c.AuthMethod = AuthMethodDirectProfile },
	} {
		t.Run(name, func(t *testing.T) {
			claims := issued
			tamper(&claims)
			if accountRefreshBindingHolds(&claims, session, user) {
				t.Fatalf("refresh accepted claims %#v", claims)
			}
		})
	}
}
