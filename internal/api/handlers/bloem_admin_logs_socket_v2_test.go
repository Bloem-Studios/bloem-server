package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
)

type bloemAdminLogsFixture struct {
	authority *bloemSocketFixture
	inner     *AdminLogsSocketV2
	socket    *BloemAdminLogsSocketV2
	endpoint  string
	origin    string
}

// newBloemAdminLogsFixture is Silo's administrator log stream socket with the
// validator the router gives it, over Bloem's session, account, tenant and
// viewer reads for an administrator. The viewer read needs the request
// tenant, as the production resolver does.
func newBloemAdminLogsFixture(t *testing.T, wrap bool) *bloemAdminLogsFixture {
	t.Helper()
	inner, _, _, _ := adminLogsSocketFixture(t)
	a, _ := newBloemProfileSocketFixture(t)
	a.users.user.Role, a.claims.Role, a.identity.Role = "admin", "admin", "admin"
	inner.Validate = newSocketAuthorityValidator(a.sessions, a.users, a.viewer,
		apimw.NewActingAdminDecider(func(context.Context, int, string) (bool, bool, error) { return true, true, nil }, nil))
	f := &bloemAdminLogsFixture{authority: a, inner: inner}
	if wrap {
		f.socket = WrapBloemAdminLogsSocketV2(inner, a.sessions, a.users, a.tenants, a.tenants)
	}
	server := httptest.NewServer(inner)
	t.Cleanup(server.Close)
	inner.PublicOrigin = server.URL
	f.origin = server.URL
	f.endpoint = "ws" + strings.TrimPrefix(server.URL, "http") + "?stream=app"
	return f
}

// requestContext is the authenticated ticket request: claims, tenant and the
// household profile the identity names.
func (f *bloemAdminLogsFixture) requestContext() context.Context {
	return apimw.SetProfileID(f.authority.ctx, f.authority.identity.ProfileID)
}

func (f *bloemAdminLogsFixture) mint(t *testing.T) string {
	t.Helper()
	mint := f.inner.Mint
	if f.socket != nil {
		mint = f.socket.Mint
	}
	ticket, err := mint(f.requestContext(), f.authority.identity)
	if err != nil {
		t.Fatalf("authenticated mint: %v", err)
	}
	return ticket
}

// dial opens the log stream the way the admin page does: no tenant, only the
// single-use ticket in the subprotocol.
func (f *bloemAdminLogsFixture) dial(t *testing.T, ticket string) (*websocket.Conn, int, string) {
	t.Helper()
	dialer := websocket.Dialer{Subprotocols: []string{AdminLogsSocketProtocol, eventsTicketProtocolPrefix + ticket}}
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
func TestBloemAdminLogsSocketWithoutTenantAuthorityRefusesUpgrade(t *testing.T) {
	f := newBloemAdminLogsFixture(t, false)
	conn, status, body := f.dial(t, f.mint(t))
	if conn != nil || status != http.StatusServiceUnavailable || body != "realtime authority could not be checked" {
		t.Fatalf("unwrapped log stream: HTTP %d %q, want 503 realtime authority could not be checked", status, body)
	}
}

func TestBloemAdminLogsSocketContextlessHandshake(t *testing.T) {
	f := newBloemAdminLogsFixture(t, true)
	ticket := f.mint(t)
	conn, status, body := f.dial(t, ticket)
	if conn == nil || status != http.StatusSwitchingProtocols || conn.Subprotocol() != AdminLogsSocketProtocol {
		t.Fatalf("contextless handshake: HTTP %d %q", status, body)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// No repository is wired in this fixture; the stream loop answers with its
	// error frame, which proves the upgrade reached it.
	if _, frame, err := conn.ReadMessage(); err != nil || !strings.Contains(string(frame), `"type":"error"`) {
		t.Fatalf("first frame %s %v", frame, err)
	}
	if replay, status, _ := f.dial(t, ticket); replay != nil || status != http.StatusUnauthorized {
		t.Fatalf("single-use credential replayed: HTTP %d", status)
	}
}

func TestBloemAdminLogsSocketMintRequiresAuthenticatedTenant(t *testing.T) {
	f := newBloemAdminLogsFixture(t, true)
	if ticket, err := f.socket.Mint(t.Context(), f.authority.identity); err == nil || ticket != "" {
		t.Fatal("contextless mint accepted")
	}
}

// A tenant that changed after the mint ends the credential (401); a tenant
// read that fails is one the client retries (503).
func TestBloemAdminLogsSocketRechecksTenantAtUpgrade(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*bloemSocketFixture)
		status int
	}{
		"policy changed":     {change: func(a *bloemSocketFixture) { a.tenants.tenant.PolicyRevision++ }, status: http.StatusUnauthorized},
		"tenant gone":        {change: bloemSocketCheckCases["tenant gone"].change, status: http.StatusUnauthorized},
		"tenant read failed": {change: bloemSocketCheckCases["tenant read failed"].change, status: http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBloemAdminLogsFixture(t, true)
			ticket := f.mint(t)
			tc.change(f.authority)
			if conn, status, body := f.dial(t, ticket); conn != nil || status != tc.status {
				t.Fatalf("%s: HTTP %d %q, want %d", name, status, body, tc.status)
			}
		})
	}
}
