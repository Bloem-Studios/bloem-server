package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Silo-Server/silo-server/internal/auth"
)

type audienceTicketSessionValidator struct{}

func (audienceTicketSessionValidator) IsValid(context.Context, string) (bool, error) {
	return true, nil
}

func TestExtractBearerTokenRejectsGeneralQueryCredential(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.test/api/v1/events/ws?token=account-secret", nil)
	if token, ok := extractBearerToken(request); ok || token != "" {
		t.Fatalf("query credential accepted: %q", token)
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
