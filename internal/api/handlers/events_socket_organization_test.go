package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/organizations"
	"github.com/gorilla/websocket"
)

type socketOrganizationBoundary struct{ state atomic.Int32 }

func (b *socketOrganizationBoundary) ResolveViewerBoundary(context.Context, int64) (organizations.ViewerBoundary, error) {
	switch b.state.Load() {
	case 1:
		return organizations.ViewerBoundary{OrganizationID: 10, AccessRevision: 2, AllowedLibraryIDs: []int{11}}, nil
	case 2:
		return organizations.ViewerBoundary{}, errors.New("organization suspended")
	default:
		return organizations.ViewerBoundary{OrganizationID: 10, AccessRevision: 1, AllowedLibraryIDs: []int{11, 33}}, nil
	}
}

// Exercise the production authority validator and actual websocket with the
// organization wrapper. No organization-specific socket protocol is needed.
func TestEventsSocketClosesOnOrganizationAuthorityChange(t *testing.T) {
	for _, state := range []int32{1, 2} {
		name := "grant revoked"
		if state == 2 {
			name = "suspended"
		}
		t.Run(name, func(t *testing.T) {
			base, _ := socketTestHandler()
			users := &socketUserFixture{user: models.User{ID: 7, OrganizationID: 10, Role: "user", Enabled: true}}
			boundary := &socketOrganizationBoundary{}
			viewer := organizations.NewViewerResolver(users, boundary, &socketViewerFixture{scope: access.Scope{UserID: 7, ProfileID: "profile", ProfileVerified: true}})
			h := NewEventsSocketV2(base.Events, base.Tickets, &socketSessionFixture{valid: true}, users, viewer, func(context.Context, int, string) (bool, bool, error) { return true, true, nil }, "")
			h.checkInterval = 10 * time.Millisecond
			identity := evt.SocketIdentity{UserID: 7, Role: "user", SessionID: "session", ProfileID: "profile", AccessExpiresAt: time.Now().Add(time.Minute)}
			ticket, err := h.Mint(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(h)
			defer server.Close()
			dialer := websocket.Dialer{Subprotocols: []string{EventsSocketProtocol, eventsTicketProtocolPrefix + ticket}}
			conn, resp, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"?channels=user_state", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer resp.Body.Close()
			if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, body, err := conn.ReadMessage()
			if err != nil || !strings.Contains(string(body), `"type":"hello"`) {
				t.Fatalf("authorized hello: %s %v", body, err)
			}
			boundary.state.Store(state)
			for {
				_, _, err = conn.ReadMessage()
				if err != nil {
					break
				}
			}
			if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				t.Fatalf("unexpected closure: %v", err)
			}
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				t.Fatalf("socket remained open: %v", err)
			}
			if _, err := h.Mint(t.Context(), identity); state == 2 && err == nil {
				t.Fatal("suspended organization minted a new ticket")
			}
		})
	}
}
