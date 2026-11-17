package handlers

// Bloem-owned. Live updates for the organization pages: a ticket minted with
// the administrative context pass, and the socket it opens
// (docs/design/bloem-org-live.md). The socket only says which item changed;
// the client re-reads it through the REST routes.
//
// Authority is the pass, not an account ticket. The ticket carries the pass's
// claims, the socket is registered under the one organization they name, and
// the claims are re-run through AdminContextMiddleware.Authorize, the code the
// REST routes run, every few seconds for as long as the socket stays open.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/orglive"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/Silo-Server/silo-server/internal/wsconn"
)

const (
	// OrgLiveProtocol is the subprotocol the server selects. The staff apps'
	// other servers speak it too.
	OrgLiveProtocol             = "bloem-live.v1"
	orgLiveTicketProtocolPrefix = "ticket."

	// SocketAudienceOrgLive keeps a ticket minted for this socket from opening
	// any other socket that shares the ticket store's shape, and the reverse.
	SocketAudienceOrgLive = "bloem_org_live"
	// orgLiveBindingVersion versions the claims a ticket carries.
	orgLiveBindingVersion = 1
	// orgLiveRole names the credential in a ticket's role field; the pass's
	// own authority is what the revalidation reads.
	orgLiveRole = "organization_live"

	// Close codes (docs/design/bloem-org-live.md).
	OrgLiveCloseInvalidTicket = 4401
	OrgLiveCloseAccessLost    = 4403
	OrgLiveCloseExpired       = 4408
	OrgLiveCloseTooMany       = 4429

	orgLiveMaxClientFrame    = 4 * 1024
	orgLiveMaxLifetime       = time.Hour
	orgLiveTicketsPerMinute  = 30
	orgLiveHeartbeat         = 25 * time.Second
	orgLiveRecheck           = 15 * time.Second
	orgLiveCoalesceWindow    = 250 * time.Millisecond
	orgLiveRecheckTimeout    = 3 * time.Second
	orgLiveRecheckStrikes    = 3
	orgLiveRetryAfterSeconds = 5
)

// OrgLiveAuthorizer revalidates an administrative context pass.
// *middleware.AdminContextMiddleware satisfies it.
type OrgLiveAuthorizer interface {
	Authorize(context.Context, auth.AdminContextClaims) (context.Context, *apimw.AdminContextRefusal)
}

// BloemOrganizationLive serves the organization live ticket route and socket.
type BloemOrganizationLive struct {
	Bus        *orglive.Bus
	Tickets    *evt.SocketTicketStore
	Authorizer OrgLiveAuthorizer
	// Channels are the channels this server serves; the others answer
	// "unavailable". The ticket response lists them.
	Channels []orglive.Channel
	// PublicOrigin is the configured external origin, never a forwarded header.
	PublicOrigin   string
	publicOrigin   atomic.Pointer[string]
	overlayOrigins atomic.Pointer[OverlayOriginSource]

	// Timings, overridable by tests. Zero means the default.
	Heartbeat      time.Duration
	RecheckEvery   time.Duration
	CoalesceWindow time.Duration

	// Start, when set, brings up the change feed the first time it is called
	// and is idempotent. The routes call it before they look at the feed's
	// health, so a node that nobody uses never opens the feed's connection, and
	// a node that a socket reaches without a ticket from it still has one.
	Start func()

	limiter ticketLimiter
}

// orgLiveStartWait bounds how long a request waits for a feed it just started.
const orgLiveStartWait = time.Second

// ensureFeed starts the change feed if it is not running and waits briefly for
// it to come up. A feed that does not come up leaves the bus unhealthy, which
// the caller answers with 503.
func (h *BloemOrganizationLive) ensureFeed(ctx context.Context) {
	if h.Start == nil || h.Bus.Healthy() {
		return
	}
	h.Start()
	deadline := time.NewTimer(orgLiveStartWait)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for !h.Bus.Healthy() {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-tick.C:
		}
	}
}

// NewBloemOrganizationLive returns the handler. Any nil dependency leaves it
// unavailable: the routes answer 503 and the capability token is not offered.
func NewBloemOrganizationLive(bus *orglive.Bus, tickets *evt.SocketTicketStore, authorizer OrgLiveAuthorizer, channels []orglive.Channel, publicURL string) *BloemOrganizationLive {
	return &BloemOrganizationLive{Bus: bus, Tickets: tickets, Authorizer: authorizer, Channels: channels, PublicOrigin: publicURL}
}

// Available reports whether the routes can serve at all.
func (h *BloemOrganizationLive) Available() bool {
	return h != nil && h.Bus != nil && h.Tickets != nil && h.Authorizer != nil && len(h.Channels) > 0
}

// SetPublicOrigin updates the browser origin accepted by new handshakes.
func (h *BloemOrganizationLive) SetPublicOrigin(origin string) {
	normalized := strings.TrimRight(origin, "/")
	h.publicOrigin.Store(&normalized)
}

// SetOverlayOrigins installs the source of overlay origins accepted next to
// the public origin.
func (h *BloemOrganizationLive) SetOverlayOrigins(source OverlayOriginSource) {
	h.overlayOrigins.Store(&source)
}

func (h *BloemOrganizationLive) validOrigin(r *http.Request) bool {
	public := h.PublicOrigin
	if origin := h.publicOrigin.Load(); origin != nil {
		public = *origin
	}
	return socketOriginAllowed(r, public, overlayOriginsFrom(h.overlayOrigins.Load()))
}

func (h *BloemOrganizationLive) heartbeat() time.Duration {
	if h.Heartbeat > 0 {
		return h.Heartbeat
	}
	return orgLiveHeartbeat
}

func (h *BloemOrganizationLive) recheckEvery() time.Duration {
	if h.RecheckEvery > 0 {
		return h.RecheckEvery
	}
	return orgLiveRecheck
}

func (h *BloemOrganizationLive) coalesceWindow() time.Duration {
	if h.CoalesceWindow > 0 {
		return h.CoalesceWindow
	}
	return orgLiveCoalesceWindow
}

// orgLiveBinding is what a ticket carries from the mint to the socket: the
// claims of the pass that minted it, never the pass itself.
type orgLiveBinding struct {
	Version int                     `json:"version"`
	Claims  auth.AdminContextClaims `json:"claims"`
}

// orgLiveFingerprint summarizes the authority a pass holds. A ticket whose
// fingerprint does not match its claims was not minted by this route.
func orgLiveFingerprint(claims auth.AdminContextClaims) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("org-live|%d|%s|%s|%s|%d|%d|%s|%s",
		claims.AccountID, claims.OrganizationID, claims.MembershipID, claims.SupportAccessID,
		claims.PolicyRevision, claims.SecurityRevision, claims.EffectiveAuthority, claims.SessionID)))
	return hex.EncodeToString(sum[:])
}

// OrgLiveTicket is the answer of the ticket route.
type OrgLiveTicket struct {
	Ticket               string    `json:"ticket"`
	ExpiresAt            time.Time `json:"expires_at"`
	Protocol             string    `json:"protocol"`
	Channels             []string  `json:"channels"`
	MaxConnectionSeconds int       `json:"max_connection_seconds"`
}

// HandleTicket handles POST /admin/organization/live/ticket. It runs behind
// the administrative context middleware, which has already validated the pass.
func (h *BloemOrganizationLive) HandleTicket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims, claimsOK := apimw.GetAdminContextClaims(r.Context())
	tenant, tenantOK := tenancy.FromContext(r.Context())
	// The organization routes' own rule: an organization-scope pass whose
	// claims and tenant agree. A platform pass is refused as they refuse it.
	if !claimsOK || !tenantOK || claims.Scope != auth.AdminScopeOrganization || claims.AccountID <= 0 || claims.OrganizationID == uuid.Nil ||
		claims.AccountID != tenant.AccountID || claims.OrganizationID != tenant.OrganizationID || claims.MembershipID != tenant.MembershipID {
		writeError(w, http.StatusForbidden, "insufficient_organization_authority", "Organization administrator authority required")
		return
	}
	if !h.Available() {
		writeError(w, http.StatusServiceUnavailable, "live_unavailable", "Live updates are unavailable")
		return
	}
	if !emptyJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid_request", "This route takes no body")
		return
	}
	h.ensureFeed(r.Context())
	if !h.Bus.Healthy() {
		w.Header().Set("Retry-After", strconv.Itoa(orgLiveRetryAfterSeconds))
		writeError(w, http.StatusServiceUnavailable, "live_unavailable", "Live updates are unavailable")
		return
	}
	if retry, ok := h.limiter.allow(claims.AccountID, time.Now()); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Round(time.Second)/time.Second))))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many live tickets; try again later")
		return
	}
	binding, err := json.Marshal(orgLiveBinding{Version: orgLiveBindingVersion, Claims: claims})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	identity := evt.SocketIdentity{
		UserID:               claims.AccountID,
		SessionID:            claims.SessionID,
		Role:                 orgLiveRole,
		EffectiveRole:        claims.EffectiveAuthority,
		AccessFingerprint:    orgLiveFingerprint(claims),
		AccessExpiresAt:      claims.ExpiresAt,
		BloemSocketAuthority: evt.BloemSocketAuthority{AuthorityBinding: string(binding)},
	}
	ticket, err := h.Tickets.Mint(r.Context(), identity)
	if err != nil {
		// A pass that expired in flight, or a ticket store that failed.
		writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "Tenant authorization is unavailable")
		return
	}
	channels := make([]string, 0, len(h.Channels))
	for _, channel := range h.Channels {
		channels = append(channels, string(channel))
	}
	expires := time.Now().Add(evt.SocketTicketTTL)
	if claims.ExpiresAt.Before(expires) {
		expires = claims.ExpiresAt
	}
	writeJSON(w, http.StatusOK, OrgLiveTicket{
		Ticket:               ticket,
		ExpiresAt:            expires.UTC(),
		Protocol:             OrgLiveProtocol,
		Channels:             channels,
		MaxConnectionSeconds: int(min(time.Until(claims.ExpiresAt), orgLiveMaxLifetime).Seconds()),
	})
}

// emptyJSONBody reports whether a request carries no body, or an empty JSON
// object or null, which some HTTP clients send for "no body".
func emptyJSONBody(r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 17))
	if err != nil {
		return false
	}
	switch string(bytes.TrimSpace(data)) {
	case "", "{}", "null":
		return true
	default:
		return false
	}
}

// ticketLimiter is a per-account sliding window.
type ticketLimiter struct {
	mu   sync.Mutex
	seen map[int][]time.Time
}

func (l *ticketLimiter) allow(account int, now time.Time) (retry time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[int][]time.Time{}
	}
	cutoff := now.Add(-time.Minute)
	recent := l.seen[account][:0]
	for _, at := range l.seen[account] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	if len(recent) >= orgLiveTicketsPerMinute {
		l.seen[account] = recent
		return recent[0].Add(time.Minute).Sub(now), false
	}
	l.seen[account] = append(recent, now)
	// Idle accounts must not accumulate: sweep when the map grows.
	if len(l.seen) > 1024 {
		for id, times := range l.seen {
			if len(times) == 0 || !times[len(times)-1].After(cutoff) {
				delete(l.seen, id)
			}
		}
	}
	return 0, true
}

// Server frames.
type orgLiveChangeFrame struct {
	Op      string `json:"op"`
	Seq     int64  `json:"seq"`
	Channel string `json:"channel"`
	ID      string `json:"id"`
	Rev     string `json:"rev"`
	Kind    string `json:"kind"`
}

type orgLiveHeartbeatFrame struct {
	Op  string `json:"op"`
	Seq int64  `json:"seq"`
}

type orgLiveResyncFrame struct {
	Op      string `json:"op"`
	Seq     int64  `json:"seq"`
	Channel string `json:"channel"`
}

type orgLiveErrorFrame struct {
	Op      string `json:"op"`
	Code    string `json:"code"`
	Channel string `json:"channel,omitempty"`
}

// orgLiveClientFrame is a client frame. Resume is accepted and ignored: every
// subscribe is answered with a resync, which closes any gap.
type orgLiveClientFrame struct {
	Op       string          `json:"op"`
	Channels []string        `json:"channels"`
	Resume   json.RawMessage `json:"resume"`
}

// ServeHTTP handles GET /organization/live/ws.
func (h *BloemOrganizationLive) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !h.Available() {
		http.Error(w, "live updates unavailable", http.StatusServiceUnavailable)
		return
	}
	// Proof travels only in the handshake protocols, never a URL or cookie.
	if r.Method != http.MethodGet || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.URL.Query().Has("token") || r.URL.Query().Has("ticket") {
		http.Error(w, "invalid handshake", http.StatusBadRequest)
		return
	}
	if !h.validOrigin(r) {
		http.Error(w, "origin refused", http.StatusForbidden)
		return
	}
	protocols := websocket.Subprotocols(r)
	if len(protocols) != 2 || protocols[0] != OrgLiveProtocol || !strings.HasPrefix(protocols[1], orgLiveTicketProtocolPrefix) {
		http.Error(w, "required subprotocol missing", http.StatusBadRequest)
		return
	}
	// Reject malformed upgrade requests before burning the credential.
	if !websocket.IsWebSocketUpgrade(r) || r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "invalid upgrade", http.StatusBadRequest)
		return
	}
	if key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key")); err != nil || len(key) != 16 {
		http.Error(w, "invalid upgrade", http.StatusBadRequest)
		return
	}
	h.ensureFeed(r.Context())
	if !h.Bus.Healthy() {
		w.Header().Set("Retry-After", strconv.Itoa(orgLiveRetryAfterSeconds))
		http.Error(w, "live updates unavailable", http.StatusServiceUnavailable)
		return
	}
	ticket := strings.TrimPrefix(protocols[1], orgLiveTicketProtocolPrefix)
	upgrader := websocket.Upgrader{Subprotocols: []string{OrgLiveProtocol}, CheckOrigin: h.validOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	h.serve(r.Context(), conn, ticket)
}

func closeOrgLive(conn *websocket.Conn, code int, reason string) {
	_ = writeWebSocketControl(conn, websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

// serve runs one connection: it consumes the ticket, registers the socket
// under the organization the ticket names, and loops until something ends it.
func (h *BloemOrganizationLive) serve(parent context.Context, conn *websocket.Conn, ticket string) {
	identity, err := h.Tickets.Consume(parent, ticket)
	if err != nil || identity.Audience != SocketAudienceOrgLive {
		closeOrgLive(conn, OrgLiveCloseInvalidTicket, "invalid_ticket")
		return
	}
	var binding orgLiveBinding
	if json.Unmarshal([]byte(identity.AuthorityBinding), &binding) != nil || binding.Version != orgLiveBindingVersion {
		closeOrgLive(conn, OrgLiveCloseInvalidTicket, "invalid_ticket")
		return
	}
	claims := binding.Claims
	// The claims must be the ones the ticket's own fields vouch for, and name
	// one organization of an organization-scope pass.
	if claims.Scope != auth.AdminScopeOrganization || claims.OrganizationID == uuid.Nil || claims.AccountID <= 0 ||
		claims.AccountID != identity.UserID || claims.SessionID != identity.SessionID ||
		orgLiveFingerprint(claims) != identity.AccessFingerprint {
		closeOrgLive(conn, OrgLiveCloseInvalidTicket, "invalid_ticket")
		return
	}
	logger := slog.With("component", "orglive", "account_id", claims.AccountID, "organization_id", claims.OrganizationID.String())

	deadline := time.Now().Add(orgLiveMaxLifetime)
	if claims.ExpiresAt.Before(deadline) {
		deadline = claims.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()

	// The socket opens only if the pass still holds, by the rules the REST
	// routes apply. A pass revoked between the mint and now stops here.
	if refusal := h.recheck(ctx, claims); refusal != nil {
		if refusal.Transient() {
			closeOrgLive(conn, websocket.CloseTryAgainLater, "live_unavailable")
		} else {
			closeOrgLive(conn, OrgLiveCloseAccessLost, "access_lost")
		}
		logger.InfoContext(ctx, "organization live socket refused", "reason", refusal.Code)
		return
	}

	mailbox := orglive.NewMailbox(0)
	unregister, err := h.Bus.Register(claims.OrganizationID, claims.AccountID, mailbox)
	switch {
	case errors.Is(err, orglive.ErrTooManyConnections):
		closeOrgLive(conn, OrgLiveCloseTooMany, "too_many_connections")
		logger.InfoContext(ctx, "organization live socket refused", "reason", "too_many_connections")
		return
	case err != nil:
		closeOrgLive(conn, websocket.CloseTryAgainLater, "live_unavailable")
		logger.InfoContext(ctx, "organization live socket refused", "reason", "feed_down")
		return
	}
	defer unregister()
	logger.InfoContext(ctx, "organization live socket opened")

	configureWebSocket(conn)
	conn.SetReadLimit(orgLiveMaxClientFrame)
	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	startWebSocketPingLoop(connCtx, func() error { return writeWebSocketControl(conn, websocket.PingMessage, nil) })

	reason := h.loop(connCtx, conn, mailbox, claims)
	logger.InfoContext(ctx, "organization live socket closed", "reason", reason)
}

// startOrgLiveReader reads the connection's client frames in the background.
// It hands them over on reads and closes done when the connection ends.
func startOrgLiveReader(ctx context.Context, conn *websocket.Conn) (reads <-chan []byte, done <-chan struct{}) {
	frames := make(chan []byte, 8)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			messageType, data, err := wsconn.ReadMessage(conn, orgLiveMaxClientFrame)
			if err != nil {
				return
			}
			if messageType != websocket.TextMessage {
				// A binary frame is not part of this protocol.
				closeOrgLive(conn, websocket.ClosePolicyViolation, "bad_frame")
				return
			}
			select {
			case frames <- data:
			case <-ctx.Done():
				return
			}
		}
	}()
	return frames, finished
}

// flushOrgLive writes everything the mailbox holds, resyncs first.
func flushOrgLive(conn *websocket.Conn, mailbox *orglive.Mailbox, next func() int64) error {
	for _, frame := range mailbox.Drain() {
		var err error
		if frame.Resync {
			err = writeWebSocketJSON(conn, orgLiveResyncFrame{Op: "resync", Seq: next(), Channel: string(frame.Channel)})
		} else {
			c := frame.Change
			err = writeWebSocketJSON(conn, orgLiveChangeFrame{Op: "change", Seq: next(), Channel: string(c.Channel), ID: c.ID, Rev: c.Rev, Kind: string(c.Kind)})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// loop is the connection's only writer of data frames.
func (h *BloemOrganizationLive) loop(ctx context.Context, conn *websocket.Conn, mailbox *orglive.Mailbox, claims auth.AdminContextClaims) string {
	reads, readDone := startOrgLiveReader(ctx, conn)
	var seq int64
	next := func() int64 { seq++; return seq }

	heartbeat := time.NewTicker(h.heartbeat())
	defer heartbeat.Stop()
	recheck := time.NewTicker(h.recheckEvery())
	defer recheck.Stop()
	var window *time.Timer
	var windowC <-chan time.Time
	defer func() {
		if window != nil {
			window.Stop()
		}
	}()
	strikes := 0

	for {
		select {
		case <-readDone:
			return "client_closed"
		case <-ctx.Done():
			// The pass, or the hour, ran out: not an error. Anything else
			// that cancels the context is the request or the server ending.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				closeOrgLive(conn, OrgLiveCloseExpired, "expired")
				return "expired"
			}
			return "server_stopped"
		case <-mailbox.Done():
			if mailbox.Reason() == orglive.ReasonShutdown {
				closeOrgLive(conn, websocket.CloseServiceRestart, "restart")
				return "restart"
			}
			closeOrgLive(conn, websocket.CloseTryAgainLater, "live_unavailable")
			return "feed_down"
		case <-recheck.C:
			refusal := h.recheck(ctx, claims)
			switch {
			case refusal == nil:
				strikes = 0
			case refusal.Transient():
				strikes++
				if strikes >= orgLiveRecheckStrikes {
					closeOrgLive(conn, websocket.CloseTryAgainLater, "live_unavailable")
					return "recheck_unavailable"
				}
			default:
				closeOrgLive(conn, OrgLiveCloseAccessLost, "access_lost")
				return "access_lost:" + refusal.Code
			}
		case <-heartbeat.C:
			if err := writeWebSocketJSON(conn, orgLiveHeartbeatFrame{Op: "heartbeat", Seq: next()}); err != nil {
				return "write_failed"
			}
		case <-mailbox.Wake():
			// Collect a burst for a moment before sending it.
			if window == nil {
				window = time.NewTimer(h.coalesceWindow())
				windowC = window.C
			}
		case <-windowC:
			window, windowC = nil, nil
			if err := flushOrgLive(conn, mailbox, next); err != nil {
				return "write_failed"
			}
		case data := <-reads:
			if reason := h.handleClientFrame(conn, mailbox, h.Channels, data); reason != "" {
				return reason
			}
			// A subscribe is answered at once, not after the window.
			if err := flushOrgLive(conn, mailbox, next); err != nil {
				return "write_failed"
			}
		}
	}
}

// recheck revalidates the pass with the REST routes' own rules.
func (h *BloemOrganizationLive) recheck(ctx context.Context, claims auth.AdminContextClaims) *apimw.AdminContextRefusal {
	checkCtx, cancel := context.WithTimeout(ctx, orgLiveRecheckTimeout)
	defer cancel()
	_, refusal := h.Authorizer.Authorize(checkCtx, claims)
	return refusal
}

// handleClientFrame applies one client frame. A non-empty return ends the
// connection with that reason.
func (h *BloemOrganizationLive) handleClientFrame(conn *websocket.Conn, mailbox *orglive.Mailbox, served []orglive.Channel, data []byte) string {
	var frame orgLiveClientFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		closeOrgLive(conn, websocket.ClosePolicyViolation, "bad_frame")
		return "bad_frame"
	}
	reject := func(code, channel string) string {
		if err := writeWebSocketJSON(conn, orgLiveErrorFrame{Op: "error", Code: code, Channel: truncateChannelName(channel)}); err != nil {
			return "write_failed"
		}
		return ""
	}
	switch frame.Op {
	case "subscribe":
		seen := map[string]bool{}
		for _, name := range frame.Channels {
			if seen[name] {
				continue
			}
			seen[name] = true
			channel, ok := orglive.ParseChannel(name)
			switch {
			case !ok:
				if reason := reject("unknown_channel", name); reason != "" {
					return reason
				}
			case !slices.Contains(served, channel):
				if reason := reject("unavailable", name); reason != "" {
					return reason
				}
			case !mailbox.Subscribed(channel) && mailbox.SubscribedCount() >= orglive.MaxChannelsPerConnection:
				if reason := reject("too_many_channels", name); reason != "" {
					return reason
				}
			default:
				mailbox.Subscribe(channel)
			}
		}
	case "unsubscribe":
		for _, name := range frame.Channels {
			channel, ok := orglive.ParseChannel(name)
			if !ok {
				if reason := reject("unknown_channel", name); reason != "" {
					return reason
				}
				continue
			}
			mailbox.Unsubscribe(channel)
		}
	default:
		closeOrgLive(conn, websocket.ClosePolicyViolation, "bad_frame")
		return "bad_frame"
	}
	return ""
}

func truncateChannelName(name string) string {
	if len(name) > 64 {
		return name[:64]
	}
	return name
}
