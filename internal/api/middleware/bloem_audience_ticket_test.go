package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

type fixedSessionValidator struct{ valid bool }

func (v fixedSessionValidator) IsValid(context.Context, string) (bool, error) { return v.valid, nil }

// consumeTicket mints a ticket from claims for the events route and serves one
// handshake through RequireAuth, returning the downstream request (nil when
// the middleware refused) and the response.
func consumeTicket(t *testing.T, sessions SessionValidator, claims *auth.Claims, profileID string, header http.Header) (*http.Request, *httptest.ResponseRecorder) {
	t.Helper()
	store := auth.NewAudienceTicketStore(nil)
	ticket, _, err := store.Mint(context.Background(), auth.NewAudienceTicket(auth.AudienceEventsWS, claims, profileID, ""))
	if err != nil {
		t.Fatal(err)
	}
	middleware := NewAuthMiddleware(nil, sessions, nil, nil)
	middleware.SetAudienceTicketStore(store)
	middleware.SetDirectProfileRouteGuard(func(*http.Request) bool { return true })
	var seen *http.Request
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/ws?ticket="+ticket, nil)
	for name, values := range header {
		request.Header[name] = values
	}
	response := httptest.NewRecorder()
	middleware.RequireAuth(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r })).ServeHTTP(response, request)
	return seen, response
}

// The handshake authenticates as the credential that minted the ticket, with
// every binding intact -- not as a generic principal that lost its auth
// method, tenant, device, and impersonator.
func TestAudienceTicketRestoresMintingCredential(t *testing.T) {
	impersonator := 3
	minted := &auth.Claims{
		UserID: 7, AccountIncarnationID: "11111111-2222-4333-8444-555555555555", Role: "user",
		SessionID: "login-session", ProfileID: "profile-1", DeviceID: "device-1", TokenType: auth.TokenTypeAccess,
		ImpersonatorUserID: &impersonator, OrganizationID: "10000000-0000-0000-0000-000000000001",
		MembershipID: "20000000-0000-0000-0000-000000000002", PolicyRevision: 5, SecurityRevision: 9,
		AuthMethod: auth.AuthMethodDirectProfile, CredentialRevision: 4,
	}
	seen, response := consumeTicket(t, fixedSessionValidator{valid: true}, minted, "profile-1", nil)
	if seen == nil {
		t.Fatalf("handshake refused: %d %s", response.Code, response.Body.String())
	}
	got := GetClaims(seen.Context())
	want := *minted
	if got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("claims = %#v\nwant %#v", got, want)
	}
	if !IsAudienceTicketAuthorized(seen.Context()) {
		t.Fatal("ticket-authorized marker missing")
	}
}

// A ticket that verified no profile must not let a header pick one: the
// handshake skips PIN verification, so a forged X-Profile-Id would otherwise
// select a PIN-protected profile.
func TestAudienceTicketIgnoresCallerProfileHeader(t *testing.T) {
	claims := &auth.Claims{UserID: 7, Role: "user", SessionID: "login-session", TokenType: auth.TokenTypeAccess}
	seen, response := consumeTicket(t, fixedSessionValidator{valid: true}, claims, "", http.Header{"X-Profile-Id": {"pin-protected"}})
	if seen == nil {
		t.Fatalf("handshake refused: %d %s", response.Code, response.Body.String())
	}
	if got := seen.Header.Get("X-Profile-Id"); got != "" {
		t.Fatalf("X-Profile-Id = %q, want removed", got)
	}

	seen, _ = consumeTicket(t, fixedSessionValidator{valid: true}, claims, "profile-1", http.Header{"X-Profile-Id": {"pin-protected"}})
	if seen == nil || seen.Header.Get("X-Profile-Id") != "profile-1" {
		t.Fatalf("X-Profile-Id not pinned to the ticket's verified profile")
	}
}

func TestAudienceTicketRequiresLiveLoginSession(t *testing.T) {
	cases := map[string]struct {
		sessions SessionValidator
		claims   *auth.Claims
	}{
		"revoked":     {fixedSessionValidator{valid: false}, &auth.Claims{UserID: 7, SessionID: "login-session", TokenType: auth.TokenTypeAccess}},
		"sessionless": {fixedSessionValidator{valid: true}, &auth.Claims{UserID: 7, TokenType: auth.TokenTypeAccess}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			seen, response := consumeTicket(t, tc.sessions, tc.claims, "", nil)
			if seen != nil || response.Code != http.StatusUnauthorized {
				t.Fatalf("handshake admitted: seen=%v status=%d", seen != nil, response.Code)
			}
		})
	}
}

// A scoped API key keeps its route allowlist through the ticket.
func TestAudienceTicketKeepsAPIKeyScopes(t *testing.T) {
	claims := &auth.Claims{UserID: 7, TokenType: auth.TokenTypeAPIKey, APIKeyID: 12, APIKeyScopes: []string{auth.ScopeLibrariesRead}}
	seen, response := consumeTicket(t, fixedSessionValidator{valid: true}, claims, "", nil)
	if seen != nil || response.Code != http.StatusForbidden {
		t.Fatalf("scoped key handshake admitted: seen=%v status=%d", seen != nil, response.Code)
	}
}
