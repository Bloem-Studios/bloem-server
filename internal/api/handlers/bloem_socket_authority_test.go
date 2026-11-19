package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// errBloemSocketDatabase stands in for a failed or timed-out database read.
var errBloemSocketDatabase = errors.New("database unavailable")

// errBloemSocketTenant is the error the tenancy store returns when
// its read fails.
var errBloemSocketTenant = fmt.Errorf("%w: load organization: %w", tenancy.ErrTenantUnavailable, context.DeadlineExceeded)

// bloemFailingImpersonator answers the impersonator's read with err and every
// other account from users.
type bloemFailingImpersonator struct {
	users bloemSocketUsers
	id    int
	err   atomic.Pointer[error]
}

func (s *bloemFailingImpersonator) GetByID(ctx context.Context, id int) (*models.User, error) {
	if err := s.err.Load(); err != nil && id == s.id {
		return nil, *err
	}
	return s.users.GetByID(ctx, id)
}

// bloemSwitchableTenant resolves through source until err is set.
type bloemSwitchableTenant struct {
	source *bloemSocketTenantFixture
	err    atomic.Pointer[error]
}

func (s *bloemSwitchableTenant) Resolve(ctx context.Context, id int, organization *uuid.UUID, legacy bool) (tenancy.Context, error) {
	if err := s.err.Load(); err != nil {
		return tenancy.Context{}, *err
	}
	return s.source.Resolve(ctx, id, organization, legacy)
}

func (s *bloemSwitchableTenant) fail(err error) { s.err.Store(&err) }

// bloemSocketCheckCases are the reads Bloem's socket check makes, each failing
// (the check could not run) or answering that the credential is gone.
var bloemSocketCheckCases = map[string]struct {
	change      func(*bloemSocketFixture)
	unavailable bool
}{
	"session read failed":      {change: func(f *bloemSocketFixture) { f.sessions.err = errBloemSocketDatabase }, unavailable: true},
	"session gone":             {change: func(f *bloemSocketFixture) { f.sessions.err = auth.ErrSessionNotFound }},
	"session missing":          {change: func(f *bloemSocketFixture) { f.sessions.missing = true }},
	"account read failed":      {change: func(f *bloemSocketFixture) { f.users.err = errBloemSocketDatabase }, unavailable: true},
	"account gone":             {change: func(f *bloemSocketFixture) { f.users.err = auth.ErrNotFound }},
	"tenant read failed":       {change: func(f *bloemSocketFixture) { f.tenants.err = errBloemSocketTenant }, unavailable: true},
	"tenant gone":              {change: func(f *bloemSocketFixture) { f.tenants.err = tenancy.ErrTenantNotFoundOrHidden }},
	"tenant suspended":         {change: func(f *bloemSocketFixture) { f.tenants.err = tenancy.ErrTenantSuspended }},
	"profile read failed":      {change: func(f *bloemSocketFixture) { f.tenants.profileErr = errBloemSocketTenant }, unavailable: true},
	"profile gone":             {change: func(f *bloemSocketFixture) { f.tenants.profileErr = tenancy.ErrTenantNotFoundOrHidden }},
	"profile moved":            {change: func(f *bloemSocketFixture) { f.tenants.profileOrganization = uuid.New() }},
	"session lookup timed out": {change: func(f *bloemSocketFixture) { f.sessions.err = context.DeadlineExceeded }, unavailable: true},
}

// assertBloemSocketRefusal checks err is the refusal the case names: one the
// client retries (authority_unavailable, 4004) when a read failed, and one
// that ends the credential (credential_invalid, 4002) when the read answered.
func assertBloemSocketRefusal(t *testing.T, err error, unavailable bool) {
	t.Helper()
	code, reason, ok := socketCloseFrame(err)
	if unavailable {
		if !errors.Is(err, evt.ErrSocketAuthorityUnavailable) || errors.Is(err, evt.ErrSocketTicket) || !ok || code != SocketCloseAuthorityUnavailable {
			t.Fatalf("failed read: error %v closes %d %q, want %d authority_unavailable", err, code, reason, SocketCloseAuthorityUnavailable)
		}
		return
	}
	if errors.Is(err, evt.ErrSocketAuthorityUnavailable) || !errors.Is(err, evt.ErrSocketTicket) || !ok || code != SocketCloseCredentialInvalid {
		t.Fatalf("gone credential: error %v closes %d %q, want %d credential_invalid", err, code, reason, SocketCloseCredentialInvalid)
	}
}

// newBloemProfileSocketFixture is a sign-in identity for a household profile,
// so every read the check makes runs, and the request context it mints from.
func newBloemProfileSocketFixture(t *testing.T) (*bloemSocketFixture, context.Context) {
	t.Helper()
	f := newBloemSocketFixture(t)
	f.identity.ProfileID, f.identity.ProfileToken = "profile", "pin-proof"
	return f, apimw.SetProfileID(f.ctx, "profile")
}

// A recheck tells a read that failed from one that found the credential gone.
func TestBloemSocketCheckTellsFailedReadFromGoneCredential(t *testing.T) {
	for name, tc := range bloemSocketCheckCases {
		t.Run(name, func(t *testing.T) {
			f, ctx := newBloemProfileSocketFixture(t)
			ticket, err := f.h.Mint(ctx, f.identity)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := f.h.Tickets.Consume(t.Context(), ticket)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(f)
			_, _, err = f.h.Validate(t.Context(), proof)
			assertBloemSocketRefusal(t, err, tc.unavailable)
		})
	}
}

// The ticket mint answers the same way, so the v2 route answers 503 (retry)
// rather than 403 when the check could not run.
func TestBloemSocketMintTellsFailedReadFromGoneCredential(t *testing.T) {
	for name, tc := range bloemSocketCheckCases {
		t.Run(name, func(t *testing.T) {
			f, ctx := newBloemProfileSocketFixture(t)
			tc.change(f)
			ticket, err := f.h.Mint(ctx, f.identity)
			if ticket != "" {
				t.Fatal("minted a ticket")
			}
			assertBloemSocketRefusal(t, err, tc.unavailable)
		})
	}
}

// The impersonator's account is read at mint and on every recheck.
func TestBloemSocketImpersonatorReadFailure(t *testing.T) {
	for name, tc := range map[string]struct {
		err         error
		unavailable bool
	}{
		"read failed": {err: errBloemSocketDatabase, unavailable: true},
		"gone":        {err: auth.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBloemSocketFixture(t)
			id := 42
			users := &bloemFailingImpersonator{id: id, users: bloemSocketUsers{7: &f.users.user, id: {ID: id, Enabled: true, Role: "admin", AccountIncarnationID: uuid.New()}}}
			f.h.users = users
			f.claims.ImpersonatorUserID, f.identity.ImpersonatorUserID, f.sessions.session.ImpersonatorUserID = &id, &id, &id
			proof, err := f.h.Tickets.Consume(t.Context(), f.mint(t))
			if err != nil {
				t.Fatal(err)
			}
			users.err.Store(&tc.err)
			_, _, err = f.h.Validate(t.Context(), proof)
			assertBloemSocketRefusal(t, err, tc.unavailable)
			_, err = f.h.Mint(f.ctx, f.identity)
			assertBloemSocketRefusal(t, err, tc.unavailable)
		})
	}
}

// An API key's ticket resolves its owner's tenant and its profile's
// organization on each check.
func TestBloemSocketKeyCheckTellsFailedReadFromGoneCredential(t *testing.T) {
	for name, tc := range map[string]struct {
		change      func(*bloemKeyTenants, *evt.SocketIdentity)
		unavailable bool
	}{
		"tenant read failed": {change: func(s *bloemKeyTenants, _ *evt.SocketIdentity) { s.err = errBloemSocketTenant }, unavailable: true},
		"tenant gone":        {change: func(s *bloemKeyTenants, _ *evt.SocketIdentity) { s.err = tenancy.ErrTenantNotFoundOrHidden }},
		"profile read failed": {change: func(s *bloemKeyTenants, _ *evt.SocketIdentity) {
			s.profileErr = errBloemSocketTenant
		}, unavailable: true},
		"profile gone": {change: func(_ *bloemKeyTenants, i *evt.SocketIdentity) { i.ProfileID = "missing" }},
	} {
		t.Run(name, func(t *testing.T) {
			keys := new(socketKeyFixture)
			keys.key.Store(&models.APIKeyMetadata{ID: 3, UserID: 7, ProfileID: "kid", Scopes: []string{auth.ScopeWatchRead}, Revision: 40})
			users := &socketUserFixture{user: models.User{ID: 7, Role: "user", Enabled: true, AccountIncarnationID: uuid.New()}}
			organization := uuid.New()
			tenants := &bloemKeyTenants{
				tenant:   tenancy.Context{AccountID: 7, OrganizationID: organization, MembershipID: uuid.New(), PolicyRevision: 1, SecurityRevision: 1, Legacy: true, OrganizationDefault: true},
				profiles: map[string]uuid.UUID{"kid": organization},
			}
			sessions := &bloemSocketSessionFixture{socketSessionFixture: socketSessionFixture{valid: true, account: users}}
			viewer := &socketViewerFixture{scope: access.Scope{UserID: 7, ProfileID: "kid", ProfileVerified: true, PolicyRevision: 1}}
			h := NewBloemEventsSocketV2(NewEventsHandler(evt.NewHub("bloem-key-check-test", nil), nil, nil, nil, nil, nil), evt.NewSocketTicketStore(nil, evt.SocketAudienceEvents), sessions, users, viewer, func(context.Context, apimw.ActingAdminRequest) (bool, error) { return true, nil }, keys, tenants, tenants, "")
			identity := evt.SocketIdentity{UserID: 7, Role: "user", APIKeyID: 3, ProfileID: "kid", AccessExpiresAt: time.Now().Add(evt.APIKeySocketMaxLifetime)}
			ticket, err := h.Mint(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := h.Tickets.Consume(t.Context(), ticket)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(tenants, &proof)
			_, _, err = h.Validate(t.Context(), proof)
			assertBloemSocketRefusal(t, err, tc.unavailable)
		})
	}
}

// The upgrade answers 503 when the check could not run and 401 when the
// credential is gone; an open socket closes with 4004 or 4002 for the same
// two causes.
func TestBloemEventsSocketUpgradeAndRecheckCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		wantStatus int
		wantCode   int
	}{
		"read failed": {err: errBloemSocketTenant, wantStatus: http.StatusServiceUnavailable, wantCode: SocketCloseAuthorityUnavailable},
		"tenant gone": {err: tenancy.ErrTenantNotFoundOrHidden, wantStatus: http.StatusUnauthorized, wantCode: SocketCloseCredentialInvalid},
	} {
		t.Run(name+" at upgrade", func(t *testing.T) {
			f := newBloemSocketFixture(t)
			tenants := &bloemSwitchableTenant{source: f.tenants}
			f.h.tenants = tenants
			ticket := f.mint(t)
			tenants.fail(tc.err)
			server := httptest.NewServer(f.h)
			defer server.Close()
			dialer := websocket.Dialer{Subprotocols: []string{EventsSocketProtocol, eventsTicketProtocolPrefix + ticket}}
			conn, response, err := dialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if response != nil {
				defer response.Body.Close()
			}
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil || response == nil || response.StatusCode != tc.wantStatus {
				t.Fatalf("upgrade = %v, want HTTP %d", err, tc.wantStatus)
			}
		})
		t.Run(name+" on recheck", func(t *testing.T) {
			f := newBloemSocketFixture(t)
			tenants := &bloemSwitchableTenant{source: f.tenants}
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
				t.Fatal("connection did not become live", err)
			}
			tenants.fail(tc.err)
			_, err = readUntilClosed(t, conn)
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) || closeErr.Code != tc.wantCode {
				t.Fatalf("close = %v, want %d", err, tc.wantCode)
			}
		})
	}
}

// The playback control socket shares the check: its upgrade answers 503 when
// the check could not run.
func TestBloemPlaybackControlUpgradeUnavailable(t *testing.T) {
	f := newBloemControlSocketFixture(t)
	ticket := f.mintBound(t)
	f.authority.tenants.err = errBloemSocketTenant
	conn, response, err := f.dial(t, ticket, nil) //nolint:bodyclose // dial registers response body cleanup.
	if err == nil || conn != nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upgrade with a failed read = %v, want HTTP 503", err)
	}
	_, _, err = f.socket.Mint(f.authority.ctx, f.authority.identity, f.session.ID, controlInstallation)
	assertBloemSocketRefusal(t, err, true)
}
