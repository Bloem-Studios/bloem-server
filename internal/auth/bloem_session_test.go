package auth

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Silo's session insert leaves auth_method to its column default, 'account'.
// A new session therefore opens only as an account session: any other method
// is refused rather than stored as 'account'.
func TestNewSessionOpensOnlyAsAnAccountSession(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"", "account"} {
		session := models.AuthSession{AuthMethod: method}
		if err := normalizeSessionAuthMethod(&session); err != nil || session.AuthMethod != "account" {
			t.Fatalf("method %q: err %v, stored %q; want an account session", method, err, session.AuthMethod)
		}
	}
	for _, method := range []string{AuthMethodPreviewGuest, "direct_profile"} {
		session := models.AuthSession{AuthMethod: method}
		if err := normalizeSessionAuthMethod(&session); err == nil {
			t.Fatalf("method %q: want a refusal", method)
		}
	}
}
