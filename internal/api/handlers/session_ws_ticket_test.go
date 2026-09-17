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

// mintPlaybackWSTicket mints a control ticket for sessionID as an account
// caller whose request verified verifiedProfileID ("" for none), and returns
// the consumed principal.
func mintPlaybackWSTicket(t *testing.T, manager *playback.SessionManager, sessionID, verifiedProfileID string) auth.AudienceTicket {
	t.Helper()
	store := auth.NewAudienceTicketStore(nil)
	handler := &PlaybackHandler{sessionMgr: manager, AudienceTickets: store}
	request := httptest.NewRequest(http.MethodPost, "/playback/sessions/"+sessionID+"/control/ws-ticket", nil)
	ctx := apimw.SetClaims(request.Context(), &auth.Claims{
		UserID:               7,
		AccountIncarnationID: "11111111-2222-4333-8444-555555555555",
		Role:                 "user",
		SessionID:            "login-session",
		TokenType:            auth.TokenTypeAccess,
		AuthMethod:           "password",
	})
	if verifiedProfileID != "" {
		ctx = apimw.SetProfileID(ctx, verifiedProfileID)
	}
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("session_id", sessionID)
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
	principal, err := store.Consume(context.Background(), minted.Ticket, auth.AudiencePlaybackControlWS, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestMintPlaybackWSTicketBindsOwnedSession(t *testing.T) {
	manager := playback.NewSessionManager(0, 0)
	session, err := manager.StartSession(7, "profile-1", 42, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	principal := mintPlaybackWSTicket(t, manager, session.ID, "profile-1")
	if principal.AccountID != 7 || principal.ProfileID != "profile-1" || principal.SessionID != "login-session" ||
		principal.AuthMethod != "password" || principal.AccountIncarnationID != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("principal = %#v", principal)
	}
}

// The handshake skips PIN verification because the minting request verified
// the ticket's profile. An account caller that verified no profile must not
// receive a ticket naming the session's (possibly PIN-protected) profile.
func TestMintPlaybackWSTicketDoesNotCarryUnverifiedSessionProfile(t *testing.T) {
	manager := playback.NewSessionManager(0, 0)
	session, err := manager.StartSession(7, "pin-protected", 42, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if principal := mintPlaybackWSTicket(t, manager, session.ID, ""); principal.ProfileID != "" {
		t.Fatalf("ticket profile = %q, want none", principal.ProfileID)
	}
}
