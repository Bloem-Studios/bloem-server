package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type bloemSocketViewer struct {
	policyRevision int64
	err            error
}

func (s *bloemSocketViewer) Resolve(ctx context.Context, in access.ResolveInput) (access.Scope, error) {
	if _, err := access.GroupSubjectFromContext(ctx, in.UserID, in.ProfileID); err != nil {
		return access.Scope{}, err
	}
	if in.SkipPINVerification || (in.ProfileID != "" && in.ProfileToken != "pin-proof") {
		return access.Scope{}, access.ErrProfileUnverified
	}
	return access.Scope{UserID: in.UserID, ProfileID: in.ProfileID, ProfileVerified: true, PolicyRevision: s.policyRevision}, s.err
}

type bloemSocketSessionFixture struct {
	socketSessionFixture
	session models.AuthSession
}

func (s *bloemSocketSessionFixture) GetByID(context.Context, string) (*models.AuthSession, error) {
	return &s.session, nil
}

type bloemSocketTenantFixture struct {
	tenant              tenancy.Context
	profileOrganization uuid.UUID
	err                 error
}

func (s *bloemSocketTenantFixture) Resolve(_ context.Context, accountID int, organization *uuid.UUID, legacy bool) (tenancy.Context, error) {
	if organization == nil || *organization != s.tenant.OrganizationID || accountID != s.tenant.AccountID || legacy != s.tenant.Legacy {
		return tenancy.Context{}, tenancy.ErrTenantNotFoundOrHidden
	}
	return s.tenant, s.err
}

func (s *bloemSocketTenantFixture) ProfileOrganization(context.Context, int, string) (uuid.UUID, error) {
	return s.profileOrganization, s.err
}

type bloemSocketFixture struct {
	h        *BloemEventsSocketV2
	ctx      context.Context
	identity evt.SocketIdentity
	claims   *auth.Claims
	tenants  *bloemSocketTenantFixture
	sessions *bloemSocketSessionFixture
	users    *socketUserFixture
	viewer   *bloemSocketViewer
	primary  bool
}

func newBloemSocketFixture(t *testing.T) *bloemSocketFixture {
	t.Helper()
	tenant := tenancy.Context{AccountID: 7, OrganizationID: uuid.New(), MembershipID: uuid.New(), PolicyRevision: 2, SecurityRevision: 3, OrganizationStatus: tenancy.OrganizationActive, MembershipStatus: tenancy.MembershipActive}
	user := &socketUserFixture{user: models.User{ID: 7, Enabled: true, Role: "user", AccountIncarnationID: uuid.New()}}
	until := time.Now().Add(time.Minute)
	claims := &auth.Claims{UserID: 7, Role: "user", SessionID: "session", TokenType: auth.TokenTypeAccess, AccountIncarnationID: user.user.AccountIncarnationID.String(), OrganizationID: tenant.OrganizationID.String(), MembershipID: tenant.MembershipID.String(), PolicyRevision: 2, SecurityRevision: 3, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(until)}}
	ctx := tenancy.WithContext(apimw.SetClaims(t.Context(), claims), tenant)
	f := &bloemSocketFixture{ctx: ctx, claims: claims, users: user, viewer: &bloemSocketViewer{}, primary: true,
		tenants:  &bloemSocketTenantFixture{tenant: tenant, profileOrganization: tenant.OrganizationID},
		sessions: &bloemSocketSessionFixture{socketSessionFixture: socketSessionFixture{valid: true}, session: models.AuthSession{ID: "session", UserID: 7, AuthMethod: "account", ExpiresAt: until}},
		identity: evt.SocketIdentity{UserID: 7, Role: "user", SessionID: "session", AccessExpiresAt: claims.ExpiresAt.Time},
	}
	f.h = NewBloemEventsSocketV2(NewEventsHandler(evt.NewHub("bloem-socket-test", nil), nil, nil, nil, nil, nil, nil), evt.NewSocketTicketStore(nil), f.sessions, user, f.viewer, func(context.Context, int, string) (bool, bool, error) { return f.primary, true, nil }, f.tenants, f.tenants, "")
	return f
}

func (f *bloemSocketFixture) mint(t *testing.T) string {
	t.Helper()
	ticket, err := f.h.Mint(f.ctx, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func TestBloemEventsSocketContextlessHandshake(t *testing.T) {
	f := newBloemSocketFixture(t)
	ticket := f.mint(t)
	server := httptest.NewServer(f.h)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{EventsSocketProtocol, eventsTicketProtocolPrefix + ticket}}
	conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("contextless handshake: %v (HTTP %d)", err, response.StatusCode)
	}
	defer conn.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d", response.StatusCode)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, body, err := conn.ReadMessage()
	if err != nil || !strings.Contains(string(body), `"type":"hello"`) || strings.Contains(string(body), ticket) {
		t.Fatalf("hello missing or credential exposed: %v", err)
	}
}

func TestBloemEventsSocketRestoresOnlyCapturedAuthority(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		f := newBloemSocketFixture(t)
		if legacy {
			f.claims.OrganizationID, f.claims.MembershipID = "", ""
			f.claims.PolicyRevision, f.claims.SecurityRevision = 0, 0
			f.tenants.tenant.Legacy, f.tenants.tenant.OrganizationDefault = true, true
			f.ctx = tenancy.WithContext(f.ctx, f.tenants.tenant)
		}
		f.identity.ProfileID, f.identity.ProfileToken = "profile", "pin-proof"
		f.ctx = apimw.SetProfileID(f.ctx, "profile")
		proof, err := f.h.Tickets.Consume(t.Context(), f.mint(t))
		if err != nil {
			t.Fatal(err)
		}
		forged := tenancy.WithContext(t.Context(), tenancy.Context{OrganizationID: uuid.New(), AccountID: 999})
		ctx, claims, err := f.h.Validate(forged, proof)
		if err != nil {
			t.Fatal(err)
		}
		tenant, _ := tenancy.FromContext(ctx)
		if tenant != f.tenants.tenant || claims.AccountIncarnationID != f.claims.AccountIncarnationID || claims.OrganizationID != f.claims.OrganizationID || claims.SecurityRevision != f.claims.SecurityRevision || apimw.GetProfileID(ctx) != "profile" || claims.TokenType != auth.TokenTypeAccess {
			t.Fatal("minted credential binding was lost or caller tenant was used")
		}
	}
}

type bloemRevocableTenant struct {
	source  apimw.TenantResolver
	revoked atomic.Bool
}

func (s *bloemRevocableTenant) Resolve(ctx context.Context, id int, organization *uuid.UUID, legacy bool) (tenancy.Context, error) {
	if s.revoked.Load() {
		return tenancy.Context{}, tenancy.ErrTenantSuspended
	}
	return s.source.Resolve(ctx, id, organization, legacy)
}

func TestBloemEventsSocketClosesOnTenantAuthorityLoss(t *testing.T) {
	f := newBloemSocketFixture(t)
	tenants := &bloemRevocableTenant{source: f.tenants}
	f.h.tenants = tenants
	f.h.checkInterval = 5 * time.Millisecond
	ticket := f.mint(t)
	server := httptest.NewServer(f.h)
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{EventsSocketProtocol, eventsTicketProtocolPrefix + ticket}}
	conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"?channels=catalog", nil)
	if response != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	tenants.revoked.Store(true)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "timeout") {
				t.Fatal("tenant revocation did not close the socket")
			}
			break
		}
	}
}

type bloemSocketUsers map[int]*models.User

func (s bloemSocketUsers) GetByID(_ context.Context, id int) (*models.User, error) {
	u := s[id]
	if u == nil {
		return nil, auth.ErrNotFound
	}
	return u, nil
}

func TestBloemEventsSocketImpersonationAndProfileRole(t *testing.T) {
	for _, change := range []string{"disabled", "demoted", "recreated", "unbound session"} {
		t.Run(change, func(t *testing.T) {
			f := newBloemSocketFixture(t)
			id := 42
			impersonator := &models.User{ID: id, Enabled: true, Role: "admin", AccountIncarnationID: uuid.New()}
			users := bloemSocketUsers{7: &f.users.user, id: impersonator}
			f.h.users = users
			f.claims.ImpersonatorUserID, f.identity.ImpersonatorUserID, f.sessions.session.ImpersonatorUserID = &id, &id, &id
			proof, err := f.h.Tickets.Consume(t.Context(), f.mint(t))
			if err != nil {
				t.Fatal(err)
			}
			_, claims, err := f.h.Validate(t.Context(), proof)
			if err != nil || claims.ImpersonatorUserID == nil || *claims.ImpersonatorUserID != id {
				t.Fatal("impersonation binding lost", err)
			}
			switch change {
			case "disabled":
				impersonator.Enabled = false
			case "demoted":
				impersonator.Role = "user"
			case "recreated":
				impersonator.AccountIncarnationID = uuid.New()
			case "unbound session":
				f.sessions.session.ImpersonatorUserID = nil
			}
			if _, _, err := f.h.Validate(t.Context(), proof); err == nil {
				t.Fatal("revoked impersonator accepted")
			}
		})
	}
	f := newBloemSocketFixture(t)
	f.users.user.Role, f.claims.Role, f.identity.Role = "admin", "admin", "admin"
	f.identity.ProfileID, f.identity.ProfileToken = "secondary", "pin-proof"
	f.ctx = apimw.SetProfileID(f.ctx, "secondary")
	f.primary = false
	proof, err := f.h.Tickets.Consume(t.Context(), f.mint(t))
	if err != nil {
		t.Fatal(err)
	}
	_, claims, err := f.h.Validate(t.Context(), proof)
	if err != nil || claims.Role != "user" {
		t.Fatal("secondary profile gained administrator channels", err)
	}
	f.primary = true
	if _, _, err := f.h.Validate(t.Context(), proof); err == nil {
		t.Fatal("changed profile role accepted")
	}
}

func TestBloemEventsSocketRejectsAuthorityChanges(t *testing.T) {
	for name, change := range map[string]func(*bloemSocketFixture, *evt.SocketIdentity){
		"tenant rebinding":      func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.tenant.OrganizationID = uuid.New() },
		"profile rebinding":     func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.profileOrganization = uuid.New() },
		"membership replaced":   func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.tenant.MembershipID = uuid.New() },
		"membership revision":   func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.tenant.SecurityRevision++ },
		"organization revision": func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.tenant.PolicyRevision++ },
		"tenant unavailable":    func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.err = tenancy.ErrTenantUnavailable },
		"membership suspended":  func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.tenants.err = tenancy.ErrTenantSuspended },
		"session owner":         func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.sessions.session.UserID++ },
		"session identity":      func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.sessions.session.ID = "other-session" },
		"session revoked": func(f *bloemSocketFixture, _ *evt.SocketIdentity) {
			now := time.Now()
			f.sessions.session.RevokedAt = &now
		},
		"session expired": func(f *bloemSocketFixture, _ *evt.SocketIdentity) {
			f.sessions.session.ExpiresAt = time.Now().Add(-time.Second)
		},
		"session invalid": func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.sessions.valid = false },
		"session became direct profile": func(f *bloemSocketFixture, _ *evt.SocketIdentity) {
			f.sessions.session.AuthMethod = auth.AuthMethodDirectProfile
		},
		"session device": func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.sessions.session.DeviceID = "other-device" },
		"session impersonator": func(f *bloemSocketFixture, _ *evt.SocketIdentity) {
			id := 42
			f.sessions.session.ImpersonatorUserID = &id
		},
		"account incarnation":    func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.users.user.AccountIncarnationID = uuid.New() },
		"account identity":       func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.users.user.ID++ },
		"account disabled":       func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.users.user.Enabled = false },
		"account role":           func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.users.user.Role = "admin" },
		"scope changed":          func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.viewer.policyRevision++ },
		"PIN revoked":            func(f *bloemSocketFixture, _ *evt.SocketIdentity) { f.viewer.err = access.ErrProfileUnverified },
		"profile token stripped": func(_ *bloemSocketFixture, p *evt.SocketIdentity) { p.ProfileToken = "" },
		"binding stripped":       func(_ *bloemSocketFixture, p *evt.SocketIdentity) { p.AuthorityBinding = "" },
		"binding malformed":      func(_ *bloemSocketFixture, p *evt.SocketIdentity) { p.AuthorityBinding = "{" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newBloemSocketFixture(t)
			f.identity.ProfileID, f.identity.ProfileToken = "profile", "pin-proof"
			f.ctx = apimw.SetProfileID(f.ctx, "profile")
			proof, err := f.h.Tickets.Consume(t.Context(), f.mint(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := f.h.Validate(t.Context(), proof); err != nil {
				t.Fatalf("unchanged authority: %v", err)
			}
			change(f, &proof)
			if _, _, err := f.h.Validate(t.Context(), proof); !errors.Is(err, evt.ErrSocketTicket) {
				t.Fatalf("changed authority error = %v", err)
			}
		})
	}
}

func TestBloemEventsSocketRejectsInvalidMintingPrincipal(t *testing.T) {
	for name, change := range map[string]func(*bloemSocketFixture){
		"anonymous":           func(f *bloemSocketFixture) { f.ctx = t.Context() },
		"missing tenant":      func(f *bloemSocketFixture) { f.ctx = apimw.SetClaims(t.Context(), f.claims) },
		"direct profile":      func(f *bloemSocketFixture) { f.claims.AuthMethod = auth.AuthMethodDirectProfile },
		"API key":             func(f *bloemSocketFixture) { f.claims.TokenType = auth.TokenTypeAPIKey },
		"missing incarnation": func(f *bloemSocketFixture) { f.claims.AccountIncarnationID = "" },
		"partial tenant":      func(f *bloemSocketFixture) { f.claims.MembershipID = "" },
		"stale tenant":        func(f *bloemSocketFixture) { f.claims.SecurityRevision-- },
		"foreign tenant":      func(f *bloemSocketFixture) { f.claims.OrganizationID = uuid.NewString() },
		"foreign session":     func(f *bloemSocketFixture) { f.identity.SessionID = "other" },
		"foreign profile":     func(f *bloemSocketFixture) { f.identity.ProfileID = "other" },
		"extended expiry":     func(f *bloemSocketFixture) { f.identity.AccessExpiresAt = f.identity.AccessExpiresAt.Add(time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newBloemSocketFixture(t)
			change(f)
			if _, err := f.h.Mint(f.ctx, f.identity); !errors.Is(err, evt.ErrSocketTicket) {
				t.Fatalf("mint error = %v", err)
			}
		})
	}
}
