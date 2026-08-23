package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/watchtogether"
)

func TestMintWatchTogetherWSTicketBindsRoomAndPrincipal(t *testing.T) {
	tokenService := watchtogether.NewRoomTokenService("test-secret", time.Minute)
	roomToken, _, err := tokenService.Mint(watchtogether.RoomTokenClaims{RoomID: "room-1", UserID: 7, ProfileID: "profile-1"})
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewAudienceTicketStore(nil)
	handler := &WatchTogetherHandler{TokenService: tokenService, Tickets: store}
	body, _ := json.Marshal(watchTogetherWSTicketRequest{RoomAccessToken: roomToken})
	request := httptest.NewRequest(http.MethodPost, "/watch-together/rooms/room-1/ws-ticket", bytes.NewReader(body))
	ctx := apimw.SetClaims(request.Context(), &auth.Claims{UserID: 7, ProfileID: "profile-1", Role: "user", SessionID: "session-1", TokenType: auth.TokenTypeAccess})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("room_id", "room-1")
	ctx = context.WithValue(ctx, chi.RouteCtxKey, routeContext)
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()

	handler.HandleMintRoomWSTicket(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var minted wsTicketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	principal, err := store.Consume(context.Background(), minted.Ticket, auth.AudienceWatchTogetherWS, "room-1")
	if err != nil {
		t.Fatal(err)
	}
	if principal.AccountID != 7 || principal.ProfileID != "profile-1" || principal.SessionID != "session-1" {
		t.Fatalf("principal = %#v", principal)
	}
}
