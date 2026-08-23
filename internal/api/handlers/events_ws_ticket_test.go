package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
)

func TestMintEventsWSTicketDoesNotRequireNotificationsSystem(t *testing.T) {
	store := auth.NewAudienceTicketStore(nil)
	handler := &EventsHandler{audienceTickets: store}
	request := httptest.NewRequest(http.MethodPost, "/events/ws-ticket", nil)
	request = request.WithContext(apimw.SetClaims(request.Context(), &auth.Claims{
		UserID:     7,
		Role:       "user",
		SessionID:  "session-1",
		TokenType:  auth.TokenTypeAccess,
		AuthMethod: "password",
	}))
	response := httptest.NewRecorder()

	handler.HandleMintWSTicket(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var minted wsTicketResponse
	if err := json.Unmarshal(response.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}
	principal, err := store.Consume(context.Background(), minted.Ticket, auth.AudienceEventsWS, "")
	if err != nil {
		t.Fatal(err)
	}
	if principal.AccountID != 7 || principal.SessionID != "session-1" {
		t.Fatalf("principal = %#v", principal)
	}
}
