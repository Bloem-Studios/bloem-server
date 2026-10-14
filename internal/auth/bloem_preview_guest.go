package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net"
	"strings"
	"time"
)

// AuthMethodPreviewGuest is a guest session on the shared preview account,
// opened only by exchanging a Garden-signed preview ticket.
const AuthMethodPreviewGuest = "preview_guest"

// PreviewGuestSettings is the part of the preview configuration that decides
// whether a guest session may still be used.
type PreviewGuestSettings struct {
	Enabled   bool
	Host      string
	AccountID int
}

// PreviewGuestSettingsSource reads the current preview configuration.
type PreviewGuestSettingsSource interface {
	PreviewGuestSettings(ctx context.Context) (PreviewGuestSettings, error)
}

type requestHostKey struct{}

// WithRequestHost records the request's Host header (never X-Forwarded-Host)
// for code that has only a context, such as refresh.
func WithRequestHost(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, requestHostKey{}, host)
}

// RequestHostFromContext returns the host WithRequestHost recorded, or "".
func RequestHostFromContext(ctx context.Context) string {
	host, _ := ctx.Value(requestHostKey{}).(string)
	return host
}

// HostOnly normalizes a Host header value: lower case, no port, no trailing dot.
func HostOnly(hostport string) string {
	host := strings.ToLower(strings.TrimSpace(hostport))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(strings.Trim(host, "[]"), ".")
}

// PreviewGuestAccessTTL caps a guest access token; the session's absolute
// expiry caps it further.
const PreviewGuestAccessTTL = 15 * time.Minute

// liveGuestCondition is true for a usable guest session: not revoked, before
// its absolute expiry, and used within its idle window. last_seen_at is
// written at most once a minute (SessionLastSeenTracker), well inside it.
const liveGuestCondition = `s.auth_method = 'preview_guest' AND s.revoked_at IS NULL AND s.expires_at > NOW()
	AND COALESCE(s.last_seen_at, s.created_at) > NOW() - make_interval(secs => g.idle_ttl_seconds)`

// NewPreviewGuest is a guest session the exchange is about to open.
type NewPreviewGuest struct {
	AccountID    int
	ExpiresAt    time.Time
	IdleTTL      time.Duration
	ClientBucket string
}

// PreviewGuests opens, reissues and refreshes guest sessions.
type PreviewGuests struct {
	pool     *pgxpool.Pool
	jwt      *JWTService
	settings PreviewGuestSettingsSource
	now      func() time.Time
}

func NewPreviewGuests(pool *pgxpool.Pool, jwt *JWTService, settings PreviewGuestSettingsSource) *PreviewGuests {
	return &PreviewGuests{pool: pool, jwt: jwt, settings: settings, now: time.Now}
}

// EnablePreviewGuests lets Refresh serve guest sessions, signing with this
// service's key, and returns the issuer the exchange uses.
func (s *Service) EnablePreviewGuests(pool *pgxpool.Pool, settings PreviewGuestSettingsSource) *PreviewGuests {
	s.previewGuests = NewPreviewGuests(pool, s.jwt, settings)
	return s.previewGuests
}

// CreateInTx opens a guest session in the exchange's transaction. The row
// carries no device, platform, address or user agent.
func (p *PreviewGuests) CreateInTx(ctx context.Context, tx pgx.Tx, guest NewPreviewGuest) (*TokenPair, error) {
	var role, incarnation string
	err := tx.QueryRow(ctx, `SELECT role, account_incarnation_id::text FROM users
		WHERE id = $1 AND enabled AND role <> 'admin'`, guest.AccountID).Scan(&role, &incarnation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionRevoked
	}
	if err != nil {
		return nil, fmt.Errorf("reading the preview account: %w", err)
	}
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, device_name, device_id, expires_at, auth_method)
		VALUES ($1, $2, '', '', $3, 'preview_guest')`, id, guest.AccountID, guest.ExpiresAt); err != nil {
		return nil, fmt.Errorf("opening a guest session: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_preview_guest_sessions (session_id, idle_ttl_seconds, client_bucket)
		VALUES ($1, $2, $3)`, id, int(guest.IdleTTL/time.Second), guest.ClientBucket); err != nil {
		return nil, fmt.Errorf("opening a guest session: %w", err)
	}
	return p.tokens(Claims{UserID: guest.AccountID, AccountIncarnationID: incarnation, Role: role, SessionID: id, AuthMethod: AuthMethodPreviewGuest}, guest.ExpiresAt)
}

// Reissue gives a browser that still holds a live guest session a fresh
// pair for that session instead of a new one.
func (p *PreviewGuests) Reissue(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := p.jwt.ValidateToken(refreshToken)
	if err != nil || claims.TokenType != TokenTypeRefresh {
		return nil, ErrSessionRevoked
	}
	return p.refresh(ctx, claims)
}

// refresh issues a pair for a live guest session without extending it: a
// guest session ends at its absolute expiry however active it is.
func (p *PreviewGuests) refresh(ctx context.Context, claims *Claims) (*TokenPair, error) {
	if claims.AuthMethod != AuthMethodPreviewGuest {
		return nil, ErrSessionRevoked
	}
	settings, err := p.settings.PreviewGuestSettings(ctx)
	if err != nil {
		return nil, sessionCheckUnavailable("reading preview settings", err)
	}
	if !settings.Enabled || settings.AccountID != claims.UserID || RequestHostFromContext(ctx) != settings.Host {
		return nil, ErrSessionRevoked
	}
	var role, incarnation string
	var expiresAt time.Time
	err = p.pool.QueryRow(ctx, `SELECT u.role, u.account_incarnation_id::text, s.expires_at
		FROM auth_sessions s
		JOIN bloem_preview_guest_sessions g ON g.session_id = s.id
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.user_id = $2 AND u.enabled AND u.role <> 'admin' AND `+liveGuestCondition,
		claims.SessionID, claims.UserID).Scan(&role, &incarnation, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSessionRevoked
	}
	if err != nil {
		return nil, sessionCheckUnavailable("checking guest session", err)
	}
	return p.tokens(Claims{UserID: claims.UserID, AccountIncarnationID: incarnation, Role: role, SessionID: claims.SessionID, AuthMethod: AuthMethodPreviewGuest}, expiresAt)
}

func (p *PreviewGuests) tokens(claims Claims, expiresAt time.Time) (*TokenPair, error) {
	remaining := expiresAt.Sub(p.now())
	if remaining <= 0 {
		return nil, ErrSessionRevoked
	}
	access := min(PreviewGuestAccessTTL, remaining)
	accessToken, err := p.jwt.generateToken(claims, TokenTypeAccess, access)
	if err != nil {
		return nil, fmt.Errorf("generating guest access token: %w", err)
	}
	refreshToken, err := p.jwt.generateToken(claims, TokenTypeRefresh, remaining)
	if err != nil {
		return nil, fmt.Errorf("generating guest refresh token: %w", err)
	}
	return &TokenPair{AccessToken: accessToken, RefreshToken: refreshToken, ExpiresIn: int(access / time.Second), SessionID: claims.SessionID}, nil
}

func (s *Service) refreshPreviewGuest(ctx context.Context, claims *Claims) (*TokenPair, error) {
	if s.previewGuests == nil {
		return nil, ErrSessionRevoked
	}
	return s.previewGuests.refresh(ctx, claims)
}
