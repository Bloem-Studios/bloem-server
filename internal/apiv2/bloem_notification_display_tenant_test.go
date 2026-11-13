package apiv2

import (
	"net/http"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// The Apple display route builds its own gate chain, so Bloem's tenant
// identity gate has to sit in it explicitly. Both credentials the route takes,
// the long-lived display token and an ordinary access token, must pass the
// gate once before the handler runs.
func TestBloemNotificationDisplayRunsTenantIdentity(t *testing.T) {
	deps := pilotDeps(nil, nil)
	deps.NotificationInbox = fixtureNotificationInbox()
	claims := map[string]*auth.Claims{
		"display": {UserID: 1, Role: "user", SessionID: "live", ProfileID: "p-owner", TokenType: auth.TokenTypeApplePushDisplay},
		"access":  {UserID: 1, Role: "user", SessionID: "live", TokenType: auth.TokenTypeAccess},
	}
	deps.Auth = apimw.NewAuthMiddleware(fakeTokens{claims}, fakeSessions{map[string]string{"live": "user"}}, nil, nil)
	gates := 0
	deps.TenantIdentity = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gates++; next.ServeHTTP(w, r) })
	}
	h := NewHandler(deps)
	displayPath := Prefix + "/notifications/push/apple/display/" + notificationFixtureID
	for name, headers := range map[string]map[string]string{
		"display": bearer("display"),
		"access":  with(bearer("access"), "X-Profile-Id", "p-owner"),
	} {
		gates = 0
		rec := do(t, h, http.MethodGet, displayPath, "", headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got %d: %s", name, rec.Code, rec.Body.String())
		}
		if gates != 1 {
			t.Fatalf("%s: tenant identity gate ran %d times, want 1", name, gates)
		}
	}
}
