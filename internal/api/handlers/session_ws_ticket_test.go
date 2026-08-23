package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestMintPlaybackWSTicketBindsOwnedSession(t *testing.T) {
	manager := playback.NewSessionManager(0, 0)
	session, err := manager.StartSession(7, "profile-1", 42, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewAudienceTicketStore(nil)
	handler := &PlaybackHandler{sessionMgr: manager, AudienceTickets: store}
	request := httptest.NewRequest(http.MethodPost, "/playback/sessions/"+session.ID+"/control/ws-ticket", nil)
	ctx := apimw.SetClaims(request.Context(), &auth.Claims{
		UserID:     7,
		Role:       "user",
		SessionID:  "login-session",
		TokenType:  auth.TokenTypeAccess,
		AuthMethod: "password",
	})
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("session_id", session.ID)
	request = request.WithContext(context.WithValue(ctx, chi.RouteCtxKey, routeContext))
	response := httptest.NewRecorder()

	handler.HandleMintSessionWSTicket(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var minted wsTicketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	principal, err := store.Consume(context.Background(), minted.Ticket, auth.AudiencePlaybackControlWS, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if principal.AccountID != 7 || principal.ProfileID != "profile-1" || principal.SessionID != "login-session" {
		t.Fatalf("principal = %#v", principal)
	}
}
