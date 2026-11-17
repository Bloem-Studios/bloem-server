package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/orglive"
	"github.com/Silo-Server/silo-server/internal/tenancy"
)

// orgLiveAuthority stands in for AdminContextMiddleware: it holds until told
// to refuse, as the REST routes' revalidation would.
type orgLiveAuthority struct {
	refusal atomic.Pointer[apimw.AdminContextRefusal]
	calls   atomic.Int64
}

func (a *orgLiveAuthority) Authorize(ctx context.Context, _ auth.AdminContextClaims) (context.Context, *apimw.AdminContextRefusal) {
	a.calls.Add(1)
	return ctx, a.refusal.Load()
}

func (a *orgLiveAuthority) refuse(status int, code string) {
	a.refusal.Store(&apimw.AdminContextRefusal{Status: status, Code: code, Message: code})
}

func (a *orgLiveAuthority) hold() { a.refusal.Store(nil) }

type orgLiveFixture struct {
	h         *BloemOrganizationLive
	bus       *orglive.Bus
	authority *orgLiveAuthority
	srv       *httptest.Server
}

func newOrgLiveFixture(t *testing.T) *orgLiveFixture {
	t.Helper()
	bus := orglive.NewBus()
	bus.SetHealthy(true)
	authority := &orgLiveAuthority{}
	h := NewBloemOrganizationLive(bus, evt.NewSocketTicketStore(nil, SocketAudienceOrgLive), authority, orglive.Channels, "")
	h.Heartbeat, h.RecheckEvery, h.CoalesceWindow = time.Hour, time.Hour, 20*time.Millisecond
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &orgLiveFixture{h: h, bus: bus, authority: authority, srv: srv}
}

func orgLiveClaims(org uuid.UUID) auth.AdminContextClaims {
	return auth.AdminContextClaims{
		AccountID: 41, AccountIncarnationID: uuid.New(), Scope: auth.AdminScopeOrganization, OrganizationID: org, MembershipID: uuid.New(),
		PolicyRevision: 3, SecurityRevision: 5, EffectiveAuthority: "organization_admin", SessionID: "login-1", ExpiresAt: time.Now().Add(10 * time.Minute),
	}
}

func orgLiveTenant(claims auth.AdminContextClaims) tenancy.Context {
	return tenancy.Context{AccountID: claims.AccountID, OrganizationID: claims.OrganizationID, MembershipID: claims.MembershipID}
}

// mintRequest runs the ticket route as the middleware would have left the
// request: claims and tenant in the context.
func (f *orgLiveFixture) mintRequest(claims auth.AdminContextClaims, tenant *tenancy.Context, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/bloem/v2/admin/organization/live/ticket", strings.NewReader(body))
	ctx := apimw.SetAdminContextClaims(req.Context(), claims)
	if tenant != nil {
		ctx = tenancy.WithContext(ctx, *tenant)
	}
	rec := httptest.NewRecorder()
	f.h.HandleTicket(rec, req.WithContext(ctx))
	return rec
}

func (f *orgLiveFixture) mint(t *testing.T, claims auth.AdminContextClaims) OrgLiveTicket {
	t.Helper()
	tenant := orgLiveTenant(claims)
	rec := f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d %s", rec.Code, rec.Body.String())
	}
	var ticket OrgLiveTicket
	if err := json.Unmarshal(rec.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}
	return ticket
}

// orgLiveClient reads its socket in the background: a gorilla read that times
// out is permanent, so "nothing arrived" must be asked of a channel instead.
type orgLiveClient struct {
	*websocket.Conn
	frames chan map[string]any
	done   chan struct{}
	err    error // set before done closes
}

func newOrgLiveClient(conn *websocket.Conn) *orgLiveClient {
	c := &orgLiveClient{Conn: conn, frames: make(chan map[string]any, 512), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				c.err = err
				return
			}
			var frame map[string]any
			if messageType == websocket.TextMessage && json.Unmarshal(data, &frame) == nil {
				c.frames <- frame
			}
		}
	}()
	return c
}

func (f *orgLiveFixture) dial(t *testing.T, ticket string) *orgLiveClient {
	t.Helper()
	conn, status, err := dialOrgLiveSocket(f.srv.URL, "", nil, OrgLiveProtocol, orgLiveTicketProtocolPrefix+ticket)
	if err != nil {
		t.Fatalf("dial: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return newOrgLiveClient(conn)
}

// dialOrgLiveSocket dials the socket and reports the HTTP status of a refused
// handshake. The response body is closed here.
func dialOrgLiveSocket(serverURL, path string, header http.Header, protocols ...string) (*websocket.Conn, int, error) {
	dialer := websocket.Dialer{Subprotocols: protocols, HandshakeTimeout: 3 * time.Second}
	conn, resp, err := dialer.Dial("ws"+strings.TrimPrefix(serverURL, "http")+path, header)
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	return conn, status, err
}

func readOrgLiveFrame(t *testing.T, c *orgLiveClient) map[string]any {
	t.Helper()
	select {
	case frame := <-c.frames:
		return frame
	case <-c.done:
		t.Fatalf("the socket closed while waiting for a frame: %v", c.err)
	case <-time.After(3 * time.Second):
		t.Fatal("no frame within 3s")
	}
	return nil
}

func expectOrgLiveClose(t *testing.T, c *orgLiveClient, code int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-c.frames:
			// A data frame before the close is fine.
		case <-c.done:
			var closeErr *websocket.CloseError
			if !errors.As(c.err, &closeErr) {
				t.Fatalf("expected a close %d, got %v", code, c.err)
			}
			if closeErr.Code != code {
				t.Fatalf("closed with %d %q, want %d", closeErr.Code, closeErr.Text, code)
			}
			return
		case <-deadline:
			t.Fatalf("no close %d within 3s", code)
		}
	}
}

func expectNoOrgLiveFrame(t *testing.T, c *orgLiveClient, within time.Duration) {
	t.Helper()
	select {
	case frame := <-c.frames:
		t.Fatalf("received an unexpected frame %v", frame)
	case <-c.done:
		t.Fatalf("the socket closed while it should be quiet: %v", c.err)
	case <-time.After(within):
	}
}

func subscribeOrgLive(t *testing.T, conn *orgLiveClient, channels ...string) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"op": "subscribe", "channels": channels, "resume": nil}); err != nil {
		t.Fatal(err)
	}
}

func TestOrgLiveTicketShape(t *testing.T) {
	f := newOrgLiveFixture(t)
	claims := orgLiveClaims(uuid.New())
	tenant := orgLiveTenant(claims)

	rec := f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mint = %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	ticket, _ := body["ticket"].(string)
	if len(ticket) != 43 || body["protocol"] != OrgLiveProtocol {
		t.Fatalf("body = %v", body)
	}
	channels, _ := body["channels"].([]any)
	if len(channels) != 3 || channels[0] != "org.streams" || channels[1] != "org.people" || channels[2] != "org.activity" {
		t.Fatalf("channels = %v", body["channels"])
	}
	if secs, _ := body["max_connection_seconds"].(float64); secs < 590 || secs > 600 {
		t.Fatalf("max_connection_seconds = %v", body["max_connection_seconds"])
	}
	expires, err := time.Parse(time.RFC3339, body["expires_at"].(string))
	if err != nil || time.Until(expires) > evt.SocketTicketTTL || time.Until(expires) < 20*time.Second {
		t.Fatalf("expires_at = %v (%v)", body["expires_at"], err)
	}
	// The ticket must not carry the pass or anything secret.
	if strings.Contains(rec.Body.String(), claims.SessionID) {
		t.Fatalf("the answer leaks the login session: %s", rec.Body.String())
	}

	// An empty JSON object or null is "no body"; anything else is a mistake.
	for _, ok := range []string{"{}", "null", " "} {
		if rec := f.mintRequest(claims, &tenant, ok); rec.Code != http.StatusOK {
			t.Errorf("body %q = %d", ok, rec.Code)
		}
	}
	if rec := f.mintRequest(claims, &tenant, `{"organization_id":"`+uuid.NewString()+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a body naming an organization = %d, want 400", rec.Code)
	}
}

func TestOrgLiveTicketRefusesWhatTheOrganizationRoutesRefuse(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	claims := orgLiveClaims(org)
	tenant := orgLiveTenant(claims)

	platform := claims
	platform.Scope = auth.AdminScopePlatform
	platform.OrganizationID = uuid.Nil
	foreign := tenant
	foreign.OrganizationID = uuid.New()
	otherAccount := tenant
	otherAccount.AccountID = 99
	otherMembership := tenant
	otherMembership.MembershipID = uuid.New()

	cases := map[string]*httptest.ResponseRecorder{
		"platform pass":         f.mintRequest(platform, &tenant, ""),
		"no tenant":             f.mintRequest(claims, nil, ""),
		"tenant of another org": f.mintRequest(claims, &foreign, ""),
		"tenant of another acc": f.mintRequest(claims, &otherAccount, ""),
		"another membership":    f.mintRequest(claims, &otherMembership, ""),
	}
	for name, rec := range cases {
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_organization_authority") {
			t.Errorf("%s = %d %s, want 403 insufficient_organization_authority", name, rec.Code, rec.Body.String())
		}
	}
}

func TestOrgLiveTicketUnavailableAndRateLimited(t *testing.T) {
	f := newOrgLiveFixture(t)
	claims := orgLiveClaims(uuid.New())
	tenant := orgLiveTenant(claims)

	f.bus.SetHealthy(false)
	rec := f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "live_unavailable") {
		t.Fatalf("with the feed down: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	f.bus.SetHealthy(true)

	for i := 0; i < orgLiveTicketsPerMinute; i++ {
		if rec := f.mintRequest(claims, &tenant, ""); rec.Code != http.StatusOK {
			t.Fatalf("ticket %d = %d", i, rec.Code)
		}
	}
	rec = f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("past the budget: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	// The budget is per account.
	other := orgLiveClaims(uuid.New())
	other.AccountID = 42
	otherTenant := orgLiveTenant(other)
	if rec := f.mintRequest(other, &otherTenant, ""); rec.Code != http.StatusOK {
		t.Fatalf("another account was limited too: %d", rec.Code)
	}

	unwired := &BloemOrganizationLive{}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	unwired.HandleTicket(rec, req.WithContext(tenancy.WithContext(apimw.SetAdminContextClaims(req.Context(), claims), tenant)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("an unwired handler answered %d", rec.Code)
	}
}

func TestOrgLiveSocketSubscribeResyncsThenSignalsChanges(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
	if got := conn.Subprotocol(); got != OrgLiveProtocol {
		t.Fatalf("selected subprotocol = %q", got)
	}
	expectNoOrgLiveFrame(t, conn, 80*time.Millisecond) // nothing before the first subscribe

	subscribeOrgLive(t, conn, "org.streams", "org.people")
	seen := map[string]bool{}
	var lastSeq float64
	for i := 0; i < 2; i++ {
		frame := readOrgLiveFrame(t, conn)
		if frame["op"] != "resync" {
			t.Fatalf("after subscribe: %v", frame)
		}
		seen[frame["channel"].(string)] = true
		if seq := frame["seq"].(float64); seq <= lastSeq {
			t.Fatalf("seq %v after %v", seq, lastSeq)
		}
		lastSeq = frame["seq"].(float64)
	}
	if !seen["org.streams"] || !seen["org.people"] {
		t.Fatalf("resyncs = %v", seen)
	}

	f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelPeople, ID: "7", Rev: "9", Kind: orglive.KindUpdated})
	f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelActivity, ID: "lifecycle:1", Rev: "lifecycle:1", Kind: orglive.KindCreated}) // not subscribed
	frame := readOrgLiveFrame(t, conn)
	if frame["op"] != "change" || frame["channel"] != "org.people" || frame["id"] != "7" || frame["rev"] != "9" || frame["kind"] != "updated" || frame["seq"].(float64) <= lastSeq {
		t.Fatalf("change frame = %v", frame)
	}
	expectNoOrgLiveFrame(t, conn, 100*time.Millisecond)

	// Unsubscribed means quiet.
	if err := conn.WriteJSON(map[string]any{"op": "unsubscribe", "channels": []string{"org.people"}}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool { return len(f.bus.SubscribedOrganizations(orglive.ChannelPeople)) == 0 }, "the unsubscribe was not applied")
	f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelPeople, ID: "8", Rev: "1", Kind: orglive.KindCreated})
	expectNoOrgLiveFrame(t, conn, 100*time.Millisecond)

	// Subscribing again always answers with a resync.
	subscribeOrgLive(t, conn, "org.people")
	if frame := readOrgLiveFrame(t, conn); frame["op"] != "resync" || frame["channel"] != "org.people" {
		t.Fatalf("re-subscribe: %v", frame)
	}
}

func TestOrgLiveSocketNeverShowsAnotherOrganizationsChanges(t *testing.T) {
	f := newOrgLiveFixture(t)
	orgA, orgB := uuid.New(), uuid.New()
	a := f.dial(t, f.mint(t, orgLiveClaims(orgA)).Ticket)
	claimsB := orgLiveClaims(orgB)
	claimsB.AccountID = 42
	b := f.dial(t, f.mint(t, claimsB).Ticket)
	for _, conn := range []*orgLiveClient{a, b} {
		subscribeOrgLive(t, conn, "org.streams", "org.people", "org.activity")
		for i := 0; i < 3; i++ {
			readOrgLiveFrame(t, conn) // the resyncs
		}
	}

	f.bus.Publish(orgA, orglive.Change{Channel: orglive.ChannelStreams, ID: "stream-of-a", Rev: "1", Kind: orglive.KindCreated})
	f.bus.Publish(orgA, orglive.Change{Channel: orglive.ChannelPeople, ID: "person-of-a", Rev: "1", Kind: orglive.KindDeleted})
	f.bus.ResyncOrganization(orgA, orglive.ChannelActivity)

	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		frame := readOrgLiveFrame(t, a)
		got[fmt.Sprint(frame["op"], "/", frame["channel"], "/", frame["id"])] = true
	}
	for _, want := range []string{"change/org.streams/stream-of-a", "change/org.people/person-of-a", "resync/org.activity/<nil>"} {
		if !got[want] {
			t.Errorf("organization A missed %s (got %v)", want, got)
		}
	}
	expectNoOrgLiveFrame(t, b, 200*time.Millisecond)

	// And the other way round.
	f.bus.Publish(orgB, orglive.Change{Channel: orglive.ChannelPeople, ID: "person-of-b", Rev: "1", Kind: orglive.KindCreated})
	if frame := readOrgLiveFrame(t, b); frame["id"] != "person-of-b" {
		t.Fatalf("organization B got %v", frame)
	}
	expectNoOrgLiveFrame(t, a, 200*time.Millisecond)
}

func TestOrgLiveSocketBurstsAreCoalesced(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
	subscribeOrgLive(t, conn, "org.people")
	readOrgLiveFrame(t, conn)

	// Many signals for one item in a window are one frame, the latest.
	for i := 1; i <= 20; i++ {
		f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelPeople, ID: "5", Rev: strconv.Itoa(i), Kind: orglive.KindUpdated})
	}
	frame := readOrgLiveFrame(t, conn)
	if frame["id"] != "5" || frame["rev"] != "20" || frame["kind"] != "updated" {
		t.Fatalf("coalesced frame = %v", frame)
	}
	expectNoOrgLiveFrame(t, conn, 100*time.Millisecond)

	// More distinct items than the cap become one resync, not a flood.
	for i := 0; i < orglive.DefaultMaxPending+25; i++ {
		f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelPeople, ID: strconv.Itoa(1000 + i), Rev: "1", Kind: orglive.KindCreated})
	}
	frame = readOrgLiveFrame(t, conn)
	if frame["op"] != "resync" || frame["channel"] != "org.people" {
		t.Fatalf("burst frame = %v", frame)
	}
	expectNoOrgLiveFrame(t, conn, 100*time.Millisecond)
}

func TestOrgLiveTicketAuthPaths(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	claims := orgLiveClaims(org)

	t.Run("a ticket opens once", func(t *testing.T) {
		ticket := f.mint(t, claims).Ticket
		first := f.dial(t, ticket)
		subscribeOrgLive(t, first, "org.people")
		readOrgLiveFrame(t, first)
		replay := f.dial(t, ticket)
		expectOrgLiveClose(t, replay, OrgLiveCloseInvalidTicket)
	})

	t.Run("garbage and unknown tickets close 4401", func(t *testing.T) {
		for _, ticket := range []string{"short", strings.Repeat("A", 43), strings.Repeat("-", 43)} {
			expectOrgLiveClose(t, f.dial(t, ticket), OrgLiveCloseInvalidTicket)
		}
	})

	t.Run("a ticket minted for another socket closes 4401", func(t *testing.T) {
		events := evt.NewSocketTicketStore(nil, evt.SocketAudienceEvents)
		other := f.h.Tickets
		f.h.Tickets = events
		ticket, err := events.Mint(t.Context(), evt.SocketIdentity{UserID: 41, SessionID: "s", AccessFingerprint: "x", AccessExpiresAt: time.Now().Add(time.Minute), BloemSocketAuthority: evt.BloemSocketAuthority{AuthorityBinding: "{}"}})
		f.h.Tickets = other
		if err != nil {
			t.Fatal(err)
		}
		expectOrgLiveClose(t, f.dial(t, ticket), OrgLiveCloseInvalidTicket)
	})

	t.Run("a ticket whose claims its own fields do not vouch for closes 4401", func(t *testing.T) {
		forged := func(mutate func(*auth.AdminContextClaims, *evt.SocketIdentity)) string {
			c := orgLiveClaims(org)
			identity := evt.SocketIdentity{UserID: c.AccountID, SessionID: c.SessionID, Role: orgLiveRole, AccessFingerprint: orgLiveFingerprint(c), AccessExpiresAt: c.ExpiresAt}
			mutate(&c, &identity)
			binding, _ := json.Marshal(orgLiveBinding{Version: orgLiveBindingVersion, Claims: c})
			identity.AuthorityBinding = string(binding)
			ticket, err := f.h.Tickets.Mint(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			return ticket
		}
		cases := map[string]func(*auth.AdminContextClaims, *evt.SocketIdentity){
			// Claims edited after the fingerprint was taken: another organization.
			"another organization": func(c *auth.AdminContextClaims, _ *evt.SocketIdentity) { c.OrganizationID = uuid.New() },
			"another membership":   func(c *auth.AdminContextClaims, _ *evt.SocketIdentity) { c.MembershipID = uuid.New() },
			"promoted authority":   func(c *auth.AdminContextClaims, _ *evt.SocketIdentity) { c.EffectiveAuthority = "platform_admin" },
			"a support access":     func(c *auth.AdminContextClaims, _ *evt.SocketIdentity) { c.SupportAccessID = uuid.New() },
			"another account":      func(_ *auth.AdminContextClaims, i *evt.SocketIdentity) { i.UserID = 99 },
			"another session":      func(_ *auth.AdminContextClaims, i *evt.SocketIdentity) { i.SessionID = "other-session" },
			"no organization": func(c *auth.AdminContextClaims, i *evt.SocketIdentity) {
				c.OrganizationID = uuid.Nil
				i.AccessFingerprint = orgLiveFingerprint(*c)
			},
			"a platform scope": func(c *auth.AdminContextClaims, i *evt.SocketIdentity) {
				c.Scope = auth.AdminScopePlatform
				i.AccessFingerprint = orgLiveFingerprint(*c)
			},
		}
		// Edits that leave the fingerprint stale must not open; so must edits
		// that refresh it but make the claims unusable.
		for name, mutate := range cases {
			expectOrgLiveClose(t, f.dial(t, forged(mutate)), OrgLiveCloseInvalidTicket)
			_ = name
		}
		// A ticket with no binding at all, or a newer binding version.
		noBinding, _ := f.h.Tickets.Mint(t.Context(), evt.SocketIdentity{UserID: 41, SessionID: "login-1", AccessFingerprint: "x", AccessExpiresAt: time.Now().Add(time.Minute)})
		expectOrgLiveClose(t, f.dial(t, noBinding), OrgLiveCloseInvalidTicket)
	})

	t.Run("an expired ticket closes 4401", func(t *testing.T) {
		short := orgLiveClaims(org)
		short.ExpiresAt = time.Now().Add(150 * time.Millisecond)
		ticket := f.mint(t, short).Ticket
		waitForCondition(t, func() bool { return time.Now().After(short.ExpiresAt) }, "the ticket did not expire")
		expectOrgLiveClose(t, f.dial(t, ticket), OrgLiveCloseInvalidTicket)
	})
}

func TestOrgLiveHandshakeRefusals(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	status := func(header http.Header, path string, protocols ...string) int {
		conn, code, err := dialOrgLiveSocket(f.srv.URL, path, header, protocols...)
		if err == nil {
			_ = conn.Close()
			return http.StatusSwitchingProtocols
		}
		if code == 0 {
			t.Fatalf("dial: %v", err)
		}
		return code
	}
	ticket := func() string { return f.mint(t, orgLiveClaims(org)).Ticket }

	if got := status(nil, "?ticket="+ticket(), OrgLiveProtocol, "ticket."+ticket()); got != http.StatusBadRequest {
		t.Errorf("a ticket in the URL = %d, want 400", got)
	}
	if got := status(nil, "?token=x", OrgLiveProtocol, "ticket."+ticket()); got != http.StatusBadRequest {
		t.Errorf("a token in the URL = %d, want 400", got)
	}
	if got := status(nil, "", OrgLiveProtocol); got != http.StatusBadRequest {
		t.Errorf("no ticket protocol = %d, want 400", got)
	}
	if got := status(nil, "", "silo.events.v2", "ticket."+ticket()); got != http.StatusBadRequest {
		t.Errorf("another protocol = %d, want 400", got)
	}
	if got := status(http.Header{"Origin": []string{"https://evil.example"}}, "", OrgLiveProtocol, "ticket."+ticket()); got != http.StatusForbidden {
		t.Errorf("a foreign browser origin = %d, want 403", got)
	}
	if got := status(http.Header{"Origin": []string{"null"}}, "", OrgLiveProtocol, "ticket."+ticket()); got != http.StatusSwitchingProtocols {
		t.Errorf("an opaque app origin = %d, want 101", got)
	}

	// A plain GET is not an upgrade.
	resp, err := http.Get(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a plain GET = %d, want 400", resp.StatusCode)
	}

	// None of the refusals above burned a ticket.
	kept := ticket()
	_ = status(nil, "?token=x", OrgLiveProtocol, "ticket."+kept)
	conn := f.dial(t, kept)
	subscribeOrgLive(t, conn, "org.people")
	readOrgLiveFrame(t, conn)

	// While the change feed is down the endpoint answers 503 before the upgrade.
	minted := ticket()
	f.bus.SetHealthy(false)
	if got := status(nil, "", OrgLiveProtocol, "ticket."+minted); got != http.StatusServiceUnavailable {
		t.Errorf("with the feed down = %d, want 503", got)
	}
}

func TestOrgLiveSocketClosesWhenThePassStopsHolding(t *testing.T) {
	f := newOrgLiveFixture(t)
	f.h.RecheckEvery = 30 * time.Millisecond
	org := uuid.New()

	t.Run("a pass that no longer holds at open", func(t *testing.T) {
		ticket := f.mint(t, orgLiveClaims(org)).Ticket
		f.authority.refuse(http.StatusUnauthorized, "authorization_state_stale")
		defer f.authority.hold()
		expectOrgLiveClose(t, f.dial(t, ticket), OrgLiveCloseAccessLost)
	})

	t.Run("revoked while open", func(t *testing.T) {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.people")
		readOrgLiveFrame(t, conn)
		f.authority.refuse(http.StatusUnauthorized, "authorization_state_stale")
		defer f.authority.hold()
		expectOrgLiveClose(t, conn, OrgLiveCloseAccessLost)
	})

	t.Run("an organization suspended while open", func(t *testing.T) {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.people")
		readOrgLiveFrame(t, conn)
		f.authority.refuse(http.StatusForbidden, "organization_suspended")
		defer f.authority.hold()
		expectOrgLiveClose(t, conn, OrgLiveCloseAccessLost)
	})

	t.Run("one failed check is not a lost pass", func(t *testing.T) {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.people")
		readOrgLiveFrame(t, conn)
		before := f.authority.calls.Load()
		f.authority.refuse(http.StatusServiceUnavailable, "tenant_unavailable")
		waitForCondition(t, func() bool { return f.authority.calls.Load() > before }, "no recheck ran")
		f.authority.hold()
		recovered := f.authority.calls.Load()
		waitForCondition(t, func() bool { return f.authority.calls.Load() > recovered+1 }, "no passing recheck ran")
		f.bus.Publish(org, orglive.Change{Channel: orglive.ChannelPeople, ID: "1", Rev: "1", Kind: orglive.KindUpdated})
		if frame := readOrgLiveFrame(t, conn); frame["op"] != "change" {
			t.Fatalf("after a recovered check: %v", frame)
		}
	})

	t.Run("a check that keeps failing closes with try-again-later", func(t *testing.T) {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.people")
		readOrgLiveFrame(t, conn)
		f.authority.refuse(http.StatusServiceUnavailable, "tenant_unavailable")
		defer f.authority.hold()
		expectOrgLiveClose(t, conn, websocket.CloseTryAgainLater)
	})
}

func TestOrgLiveSocketEndsWithItsPass(t *testing.T) {
	f := newOrgLiveFixture(t)
	claims := orgLiveClaims(uuid.New())
	claims.ExpiresAt = time.Now().Add(400 * time.Millisecond)
	conn := f.dial(t, f.mint(t, claims).Ticket)
	subscribeOrgLive(t, conn, "org.people")
	readOrgLiveFrame(t, conn)
	expectOrgLiveClose(t, conn, OrgLiveCloseExpired)
}

func TestOrgLiveSocketLimitsAndServerEvents(t *testing.T) {
	f := newOrgLiveFixture(t)
	org := uuid.New()
	var conns []*orgLiveClient
	for i := 0; i < orglive.MaxConnectionsPerAccount; i++ {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.people")
		readOrgLiveFrame(t, conn)
		conns = append(conns, conn)
	}
	expectOrgLiveClose(t, f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket), OrgLiveCloseTooMany)
	// Another account is a separate budget.
	other := orgLiveClaims(org)
	other.AccountID = 43
	otherConn := f.dial(t, f.mint(t, other).Ticket)
	subscribeOrgLive(t, otherConn, "org.people")
	readOrgLiveFrame(t, otherConn)

	// The feed going down closes every socket with try-again-later...
	f.bus.SetHealthy(false)
	for _, conn := range conns {
		expectOrgLiveClose(t, conn, websocket.CloseTryAgainLater)
	}
	expectOrgLiveClose(t, otherConn, websocket.CloseTryAgainLater)

	// ...and a shutdown with the restart code.
	f.bus.SetHealthy(true)
	last := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
	subscribeOrgLive(t, last, "org.people")
	readOrgLiveFrame(t, last)
	f.bus.Shutdown()
	expectOrgLiveClose(t, last, websocket.CloseServiceRestart)
}

func TestOrgLiveSocketHeartbeatAndClientFrames(t *testing.T) {
	f := newOrgLiveFixture(t)
	f.h.Heartbeat = 40 * time.Millisecond
	org := uuid.New()

	t.Run("heartbeats arrive with rising seq", func(t *testing.T) {
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		first, second := readOrgLiveFrame(t, conn), readOrgLiveFrame(t, conn)
		if first["op"] != "heartbeat" || second["op"] != "heartbeat" || second["seq"].(float64) <= first["seq"].(float64) {
			t.Fatalf("frames = %v %v", first, second)
		}
	})

	t.Run("channels the server does not know or serve answer with errors", func(t *testing.T) {
		f.h.Heartbeat = time.Hour
		f.h.Channels = []orglive.Channel{orglive.ChannelPeople, orglive.ChannelActivity} // streams unavailable
		defer func() { f.h.Channels = orglive.Channels }()
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		subscribeOrgLive(t, conn, "org.streams", "org.nope", "org.people", "org.people")
		var errorsSeen, resyncs int
		for i := 0; i < 3; i++ {
			frame := readOrgLiveFrame(t, conn)
			switch frame["op"] {
			case "error":
				errorsSeen++
				code, channel := frame["code"], frame["channel"]
				if (code != "unavailable" || channel != "org.streams") && (code != "unknown_channel" || channel != "org.nope") {
					t.Fatalf("error frame = %v", frame)
				}
			case "resync":
				resyncs++
			}
		}
		if errorsSeen != 2 || resyncs != 1 {
			t.Fatalf("errors %d resyncs %d", errorsSeen, resyncs)
		}
		expectNoOrgLiveFrame(t, conn, 100*time.Millisecond)
	})

	t.Run("a frame this protocol does not accept ends the connection", func(t *testing.T) {
		f.h.Heartbeat = time.Hour
		for name, send := range map[string]func(*websocket.Conn) error{
			"not json":   func(c *websocket.Conn) error { return c.WriteMessage(websocket.TextMessage, []byte("nope")) },
			"unknown op": func(c *websocket.Conn) error { return c.WriteJSON(map[string]any{"op": "dance"}) },
			"binary":     func(c *websocket.Conn) error { return c.WriteMessage(websocket.BinaryMessage, []byte{1, 2}) },
		} {
			conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
			if err := send(conn.Conn); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			expectOrgLiveClose(t, conn, websocket.ClosePolicyViolation)
		}
		conn := f.dial(t, f.mint(t, orgLiveClaims(org)).Ticket)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":"subscribe","channels":["`+strings.Repeat("x", orgLiveMaxClientFrame)+`"]}`)); err != nil {
			t.Fatal(err)
		}
		expectOrgLiveClose(t, conn, websocket.CloseMessageTooBig)
	})
}

func TestOrgLiveLimiterPrunesIdleAccounts(t *testing.T) {
	var l ticketLimiter
	start := time.Now()
	for account := 1; account <= 1100; account++ {
		if _, ok := l.allow(account, start); !ok {
			t.Fatal("first ticket refused")
		}
	}
	if _, ok := l.allow(5000, start.Add(2*time.Minute)); !ok {
		t.Fatal("ticket refused after the window")
	}
	l.mu.Lock()
	size := len(l.seen)
	l.mu.Unlock()
	if size > 1100 {
		t.Fatalf("limiter holds %d accounts", size)
	}
}

// The lifecycle event names the bridge reads must be the ones that publish.
func TestOrgLiveStreamEventNamesMatchTheLifecycle(t *testing.T) {
	for name, pair := range map[string][2]string{
		"started": {orglive.EventSessionStarted, LifecycleEventStarted},
		"paused":  {orglive.EventSessionPaused, LifecycleEventPaused},
		"resumed": {orglive.EventSessionResumed, LifecycleEventResumed},
		"stopped": {orglive.EventSessionStopped, LifecycleEventStopped},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: orglive reads %q, the lifecycle publishes %q", name, pair[0], pair[1])
		}
	}
}

// The change feed starts the first time something needs it, and only waits a
// moment for it: a node nobody opens an organization page on never opens its
// LISTEN connection, and a socket that reaches a node without a ticket from it
// still finds one.
func TestOrgLiveStartsTheFeedOnFirstUse(t *testing.T) {
	f := newOrgLiveFixture(t)
	f.bus.SetHealthy(false)
	var starts atomic.Int64
	f.h.Start = func() {
		starts.Add(1)
		time.AfterFunc(60*time.Millisecond, func() { f.bus.SetHealthy(true) })
	}
	claims := orgLiveClaims(uuid.New())
	tenant := orgLiveTenant(claims)

	rec := f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusOK || starts.Load() != 1 {
		t.Fatalf("first mint = %d after %d starts: %s", rec.Code, starts.Load(), rec.Body.String())
	}
	var ticket OrgLiveTicket
	_ = json.Unmarshal(rec.Body.Bytes(), &ticket)

	// Once it is up, nothing calls Start again.
	conn := f.dial(t, ticket.Ticket)
	subscribeOrgLive(t, conn, "org.people")
	readOrgLiveFrame(t, conn)
	if rec := f.mintRequest(claims, &tenant, ""); rec.Code != http.StatusOK || starts.Load() != 1 {
		t.Fatalf("second mint = %d after %d starts", rec.Code, starts.Load())
	}
}

func TestOrgLiveSocketStartsTheFeedWithoutATicketFromThisNode(t *testing.T) {
	f := newOrgLiveFixture(t)
	ticket := f.mint(t, orgLiveClaims(uuid.New())).Ticket // minted while the feed was up
	f.bus.SetHealthy(false)                               // this node's feed is not running yet
	var starts atomic.Int64
	f.h.Start = func() {
		starts.Add(1)
		time.AfterFunc(60*time.Millisecond, func() { f.bus.SetHealthy(true) })
	}
	conn := f.dial(t, ticket)
	subscribeOrgLive(t, conn, "org.people")
	if frame := readOrgLiveFrame(t, conn); frame["op"] != "resync" || starts.Load() != 1 {
		t.Fatalf("frame %v after %d starts", frame, starts.Load())
	}
}

func TestOrgLiveAnswers503WhenTheFeedDoesNotComeUp(t *testing.T) {
	f := newOrgLiveFixture(t)
	f.bus.SetHealthy(false)
	var starts atomic.Int64
	f.h.Start = func() { starts.Add(1) } // never heals
	claims := orgLiveClaims(uuid.New())
	tenant := orgLiveTenant(claims)

	began := time.Now()
	rec := f.mintRequest(claims, &tenant, "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || starts.Load() != 1 {
		t.Fatalf("mint = %d %v after %d starts", rec.Code, rec.Header(), starts.Load())
	}
	if waited := time.Since(began); waited > 3*time.Second {
		t.Fatalf("the request waited %s for a feed that did not come up", waited)
	}
}
