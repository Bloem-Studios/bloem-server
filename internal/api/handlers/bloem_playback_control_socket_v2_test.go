package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type bloemControlSocketFixture struct {
	*controlSocketFixture
	authority *bloemSocketFixture
	socket    *BloemPlaybackControlSocketV2
}

func newBloemControlSocketFixture(t *testing.T, legacy bool) *bloemControlSocketFixture {
	t.Helper()
	f := newControlSocketFixture(t)
	a := newBloemSocketFixture(t)
	a.identity.ProfileID, a.identity.ProfileToken = f.session.ProfileID, "pin-proof"
	a.ctx = apimw.SetProfileID(a.ctx, f.session.ProfileID)
	a.sessions.session.DeviceID = "shield-device"
	if legacy {
		a.claims.OrganizationID, a.claims.MembershipID = "", ""
		a.claims.PolicyRevision, a.claims.SecurityRevision = 0, 0
		a.tenants.tenant.Legacy, a.tenants.tenant.OrganizationDefault = true, true
		a.ctx = tenancy.WithContext(a.ctx, a.tenants.tenant)
	} else {
		a.claims.DeviceID = "shield-device"
	}
	socket := NewBloemPlaybackControlSocketV2(f.pb, nil, a.sessions, a.users, a.viewer,
		func(context.Context, int, string) (bool, bool, error) { return true, true, nil }, a.tenants, a.tenants, "")
	f.handler = socket.PlaybackControlSocketV2
	f.server.Close()
	router := chi.NewRouter()
	router.Get("/api/v2/playback/sessions/{session_id}/control/ws", socket.ServeHTTP)
	f.server = httptest.NewServer(router)
	t.Cleanup(f.server.Close)
	socket.PublicOrigin = f.server.URL
	return &bloemControlSocketFixture{controlSocketFixture: f, authority: a, socket: socket}
}

func (f *bloemControlSocketFixture) mintBound(t *testing.T) string {
	t.Helper()
	ticket, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, f.session.ID, controlInstallation)
	if err != nil {
		t.Fatalf("authenticated mint: %v", err)
	}
	return ticket
}

// Mint has authenticated request context; the public WebSocket upgrade has
// none. It must restore the captured authority rather than reject every owner.
func TestBloemPlaybackControlContextlessHandshake(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "tenant-bound"
		if legacy {
			name = "legacy-account"
		}
		t.Run(name, func(t *testing.T) {
			f := newBloemControlSocketFixture(t, legacy)
			ticket := f.mintBound(t)
			conn, resp, err := f.dial(t, ticket, nil) //nolint:bodyclose // dial registers response body cleanup.
			if err != nil {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("contextless handshake: HTTP %d, body %q, error %v", resp.StatusCode, body, err)
			}
			if resp.StatusCode != http.StatusSwitchingProtocols {
				t.Fatal("playback control did not upgrade")
			}
			f.hello(t, conn)
			if replay, response, err := f.dial(t, ticket, nil); replay != nil || err == nil || response.StatusCode != http.StatusUnauthorized { //nolint:bodyclose // dial registers response body cleanup.
				t.Fatal("single-use credential was replayed")
			}
		})
	}
}

func TestBloemPlaybackControlRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"session revoked", "account disabled", "account recreated", "device changed", "policy changed", "membership changed", "profile moved", "PIN changed", "playback stopped"} {
		t.Run(change, func(t *testing.T) {
			f := newBloemControlSocketFixture(t, true)
			ticket := f.mintBound(t)
			switch change {
			case "session revoked":
				f.authority.sessions.session.RevokedAt = new(time.Now())
			case "account disabled":
				f.authority.users.user.Enabled = false
			case "account recreated":
				f.authority.users.user.AccountIncarnationID = uuid.New()
			case "device changed":
				f.authority.sessions.session.DeviceID = "other-device"
			case "policy changed":
				f.authority.tenants.tenant.PolicyRevision++
			case "membership changed":
				f.authority.tenants.tenant.MembershipID = uuid.New()
			case "profile moved":
				f.authority.tenants.profileOrganization = uuid.New()
			case "PIN changed":
				f.authority.viewer.err = access.ErrProfileUnverified
			case "playback stopped":
				if err := f.manager.StopSession(f.session.ID); err != nil {
					t.Fatal(err)
				}
			}
			conn, response, err := f.dial(t, ticket, nil) //nolint:bodyclose // dial registers response body cleanup.
			if err == nil || conn != nil || response.StatusCode == http.StatusSwitchingProtocols {
				t.Fatalf("changed authority admitted: %s", change)
			}
		})
	}
}

func TestBloemPlaybackControlMintRequiresAuthenticatedTenant(t *testing.T) {
	f := newBloemControlSocketFixture(t, true)
	if ticket, _, err := f.socket.Mint(t.Context(), f.authority.identity, f.session.ID, controlInstallation); err == nil || ticket != "" {
		t.Fatal("contextless mint accepted")
	}
	f.authority.identity.ProfileToken = "wrong-pin-proof"
	if ticket, _, err := f.socket.Mint(f.authority.ctx, f.authority.identity, f.session.ID, controlInstallation); err == nil || ticket != "" {
		t.Fatal("invalid profile proof accepted")
	}
}

func TestBloemPlaybackControlClosesOnTenantAuthorityLoss(t *testing.T) {
	f := newBloemControlSocketFixture(t, true)
	tenant := &bloemRevocableTenant{source: f.authority.tenants}
	f.socket.authority.tenants = tenant
	f.socket.checkInterval = 5 * time.Millisecond
	conn, _, err := f.dial(t, f.mintBound(t), nil) //nolint:bodyclose // dial registers response body cleanup.
	if err != nil {
		t.Fatal(err)
	}
	f.hello(t, conn)
	tenant.revoked.Store(true)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
				t.Fatal("revoked tenant kept the control socket open")
			}
			break
		}
	}
}
