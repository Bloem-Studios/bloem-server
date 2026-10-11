package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/auth"
)

type audienceTicketSessionValidator struct{}

func (audienceTicketSessionValidator) IsValid(context.Context, string) (bool, error) {
	return true, nil
}

func TestExtractBearerTokenMatchesSilo(t *testing.T) {
	for _, test := range []struct {
		name, target, header, want string
	}{
		{"subtitle URL", "/api/v2/stream/session/subtitles/1.vtt?token=account-token", "", "account-token"},
		{"font URL", "/api/v2/stream/session/subtitles/1/fonts?token=account-token", "", "account-token"},
		{"header takes precedence", "/api/v2/stream/session/subtitles/1.vtt?token=url-token", "Bearer header-token", "header-token"},
		{"missing credential", "/api/v2/stream/session/subtitles/1.vtt", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.Header.Set("Authorization", test.header)
			if token, ok := extractBearerToken(request); token != test.want || ok != (test.want != "") {
				t.Fatalf("token=%q ok=%v, want %q", token, ok, test.want)
			}
		})
	}
}

func TestRequireAuthSiloQueryTokenKeepsSessionValidation(t *testing.T) {
	jwt := auth.NewJWTService("silo-query-token-test", 15*time.Minute, 24*time.Hour)
	middleware := NewAuthMiddleware(jwt, &fakeSessionValidator{roles: map[string]string{"live": "user"}}, nil, nil)
	handler := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetClaims(r.Context())
		if claims == nil || claims.UserID != 42 || claims.SessionID != "live" {
			t.Fatalf("unexpected authenticated viewer: %#v", claims)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, test := range []struct {
		name, session string
		want          int
	}{
		{"active session", "live", http.StatusNoContent},
		{"revoked session", "revoked", http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			token, err := jwt.GenerateAccessToken(42, "user", test.session)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v2/stream/session/subtitles/1.vtt?token="+token, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d", response.Code, test.want)
			}
		})
	}
}

func TestAudienceTicketRouteBindsWatchTogetherRoom(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.test/api/v1/watch-together/rooms/room-7/ws?ticket=t", nil)
	audience, resource, ok := audienceTicketRoute(request)
	if !ok || audience != auth.AudienceWatchTogetherWS || resource != "room-7" {
		t.Fatalf("route = %q %q %v", audience, resource, ok)
	}
}

func TestAudienceTicketRouteBindsPlaybackSession(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.test/api/v1/playback/sessions/session-7/control/ws?ticket=t", nil)
	audience, resource, ok := audienceTicketRoute(request)
	if !ok || audience != auth.AudiencePlaybackControlWS || resource != "session-7" {
		t.Fatalf("route = %q %q %v", audience, resource, ok)
	}
}

func TestRequireAuthConsumesAudienceTicketAndRejectsReplay(t *testing.T) {
	store := auth.NewAudienceTicketStore(nil)
	ticket, _, err := store.Mint(context.Background(), auth.AudienceTicket{
		Audience:  auth.AudienceEventsWS,
		AccountID: 7,
		ProfileID: "profile-1",
		Role:      "user",
		SessionID: "login-session",
		TokenType: auth.TokenTypeAccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	middleware := NewAuthMiddleware(nil, audienceTicketSessionValidator{}, nil, nil)
	middleware.SetAudienceTicketStore(store)
	called := false
	handler := middleware.RequireAuth(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		called = true
		claims := GetClaims(request.Context())
		if claims == nil || claims.UserID != 7 || claims.ProfileID != "profile-1" {
			t.Fatalf("claims = %#v", claims)
		}
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/ws?ticket="+ticket, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !called || response.Code != http.StatusOK {
		t.Fatalf("first request called=%v status=%d", called, response.Code)
	}

	called = false
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/events/ws?ticket="+ticket, nil))
	if called || response.Code != http.StatusUnauthorized {
		t.Fatalf("replay called=%v status=%d", called, response.Code)
	}
}
