package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/watchtogether"
)

type bloemRoomSocketFixture struct {
	authority *bloemSocketFixture
	inner     *WatchTogetherSocketV2
	socket    *BloemWatchTogetherSocketV2
	endpoint  string
	origin    string
	proof     string
}

// newBloemRoomSocketFixture is Silo's room socket with the validator the
// router gives it, over Bloem's session, account, tenant and viewer reads. The
// viewer read needs the request tenant, as the production resolver does.
func newBloemRoomSocketFixture(t *testing.T, wrap bool) *bloemRoomSocketFixture {
	t.Helper()
	inner, _, _ := roomSocketHandler(t)
	a, _ := newBloemProfileSocketFixture(t)
	inner.Validate = refuseRemotePlaybackSocket(newSocketAuthorityValidator(a.sessions, a.users, a.viewer,
		apimw.NewActingAdminDecider(func(context.Context, int, string) (bool, bool, error) { return true, true, nil }, nil)))
	f := &bloemRoomSocketFixture{authority: a, inner: inner}
	if wrap {
		f.socket = WrapBloemWatchTogetherSocketV2(inner, a.sessions, a.users, a.tenants, a.tenants)
	}
	server := roomSocketServer(t, inner)
	f.endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "/rooms/room/ws"
	f.origin = server.URL
	proof, _, err := inner.Room.TokenService.Mint(watchtogether.RoomTokenClaims{RoomID: "room", UserID: a.identity.UserID, ProfileID: a.identity.ProfileID})
	if err != nil {
		t.Fatal(err)
	}
	f.proof = proof
	return f
}

// requestContext is the authenticated ticket request: claims, tenant and the
// household profile the identity names.
func (f *bloemRoomSocketFixture) requestContext() context.Context {
	return apimw.SetProfileID(f.authority.ctx, f.authority.identity.ProfileID)
}

func (f *bloemRoomSocketFixture) mint(t *testing.T) string {
	t.Helper()
	mint := f.inner.MintRoomSocket
	if f.socket != nil {
		mint = f.socket.MintRoomSocket
	}
	ticket, _, err := mint(f.requestContext(), "room", f.proof, f.authority.identity)
	if err != nil {
		t.Fatalf("authenticated mint: %v", err)
	}
	return ticket
}

// dial opens the room socket the way a browser does: no tenant, only the
// single-use ticket in the subprotocol.
func (f *bloemRoomSocketFixture) dial(t *testing.T, ticket string) (*websocket.Conn, int, string) {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{watchtogether.RoomSocketProtocol, eventsTicketProtocolPrefix + ticket}}
	conn, resp, err := dialer.DialContext(t.Context(), f.endpoint, http.Header{"Origin": []string{f.origin}})
	if resp == nil {
		t.Fatalf("dial: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if conn != nil {
		t.Cleanup(func() { _ = conn.Close() })
	}
	return conn, resp.StatusCode, strings.TrimSpace(string(body))
}

// Silo's socket alone mints (the ticket route has the request tenant) and
// then refuses its own upgrade, which has none: the production failure.
func TestBloemRoomSocketWithoutTenantAuthorityRefusesUpgrade(t *testing.T) {
	f := newBloemRoomSocketFixture(t, false)
	conn, status, body := f.dial(t, f.mint(t))
	if conn != nil || status != http.StatusServiceUnavailable || body != "realtime authority could not be checked" {
		t.Fatalf("unwrapped room socket: HTTP %d %q, want 503 realtime authority could not be checked", status, body)
	}
}

func TestBloemRoomSocketContextlessHandshake(t *testing.T) {
	f := newBloemRoomSocketFixture(t, true)
	ticket := f.mint(t)
	conn, status, body := f.dial(t, ticket)
	if conn == nil || status != http.StatusSwitchingProtocols {
		t.Fatalf("contextless handshake: HTTP %d %q", status, body)
	}
	if conn.Subprotocol() != watchtogether.RoomSocketProtocol {
		t.Fatalf("subprotocol = %q", conn.Subprotocol())
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, frame, err := conn.ReadMessage(); err != nil || !strings.Contains(string(frame), `"type":"snapshot"`) {
		t.Fatalf("first frame %s %v", frame, err)
	}
	if replay, status, _ := f.dial(t, ticket); replay != nil || status != http.StatusUnauthorized {
		t.Fatalf("single-use credential replayed: HTTP %d", status)
	}
}

func TestBloemRoomSocketMintRequiresAuthenticatedTenant(t *testing.T) {
	f := newBloemRoomSocketFixture(t, true)
	if ticket, _, err := f.socket.MintRoomSocket(t.Context(), "room", f.proof, f.authority.identity); err == nil || ticket != "" {
		t.Fatal("contextless mint accepted")
	}
	if ticket, _, err := f.socket.MintRoomSocket(f.requestContext(), "other", f.proof, f.authority.identity); err == nil || ticket != "" {
		t.Fatal("foreign room proof accepted")
	}
}

// A tenant that changed after the mint ends the credential (401); a tenant
// read that fails is one the client retries (503).
func TestBloemRoomSocketRechecksTenantAtUpgrade(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*bloemSocketFixture)
		status int
	}{
		"policy changed":     {change: func(a *bloemSocketFixture) { a.tenants.tenant.PolicyRevision++ }, status: http.StatusUnauthorized},
		"profile moved":      {change: bloemSocketCheckCases["profile moved"].change, status: http.StatusUnauthorized},
		"tenant gone":        {change: bloemSocketCheckCases["tenant gone"].change, status: http.StatusUnauthorized},
		"tenant read failed": {change: bloemSocketCheckCases["tenant read failed"].change, status: http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBloemRoomSocketFixture(t, true)
			ticket := f.mint(t)
			tc.change(f.authority)
			if conn, status, body := f.dial(t, ticket); conn != nil || status != tc.status {
				t.Fatalf("%s: HTTP %d %q, want %d", name, status, body, tc.status)
			}
		})
	}
}
