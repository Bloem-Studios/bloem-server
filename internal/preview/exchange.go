package preview

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/auth"
)

// MaxGuestsPerClient is the live guest sessions one IPv4 address or IPv6 /64
// may hold; a further exchange evicts that client's oldest.
const MaxGuestsPerClient = 3

var (
	// ErrUnavailable: the preview is off, or this is not its host.
	ErrUnavailable = errors.New("preview: unavailable")
	// ErrBusy: every guest slot is in use by an active session.
	ErrBusy = errors.New("preview: every guest slot is in use")
)

type GuestIssuer interface {
	CreateInTx(ctx context.Context, tx pgx.Tx, guest auth.NewPreviewGuest) (*auth.TokenPair, error)
	ReissueInTx(ctx context.Context, tx pgx.Tx, refreshToken string) (*auth.TokenPair, error)
}

// ServerID names this server; tickets must carry it as their audience.
type ServerID interface {
	Resolve(ctx context.Context) (string, error)
}

type ExchangeInput struct {
	Ticket       string
	RefreshToken string // the browser's current guest refresh token, if any
	Host         string // auth.HostOnly(r.Host)
	ClientIP     string
}

type ExchangeResult struct {
	Tokens    *auth.TokenPair
	ExpiresAt time.Time
	Item      string
	Ref       string
	Reused    bool
	// FreePlay reports that the session is bound to a free-play grant.
	FreePlay bool
}

type Exchanger struct {
	pool     *pgxpool.Pool
	guests   GuestIssuer
	serverID ServerID
	buckets  *ClientBuckets
	now      func() time.Time

	sweepMu   sync.Mutex
	nextSweep time.Time
}

func NewExchanger(pool *pgxpool.Pool, guests GuestIssuer, serverID ServerID, buckets *ClientBuckets) *Exchanger {
	return &Exchanger{pool: pool, guests: guests, serverID: serverID, buckets: buckets, now: time.Now}
}

const liveGuests = `FROM auth_sessions s JOIN bloem_preview_guest_sessions g ON g.session_id = s.id
	WHERE s.auth_method = 'preview_guest' AND s.revoked_at IS NULL AND s.expires_at > now()
	  AND COALESCE(s.last_seen_at, s.created_at) > now() - make_interval(secs => g.idle_ttl_seconds)`

// Exchange turns a launch ticket into a guest session. It holds the settings
// lock for its whole transaction, so capacity is counted exactly and a kill
// switch never interleaves.
func (e *Exchanger) Exchange(ctx context.Context, in ExchangeInput) (ExchangeResult, error) {
	defer e.maybeSweep(ctx)
	serverID, err := e.serverID.Resolve(ctx)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: resolving server id: %w", err)
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, settingsLockKey); err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
	}
	settings, err := getSettings(ctx, tx)
	if err != nil {
		return ExchangeResult{}, err
	}
	if !settings.Enabled || in.Host == "" || in.Host != settings.Host {
		return ExchangeResult{}, ErrUnavailable
	}

	ticket, err := VerifyTicket(in.Ticket, settings.TicketPublicKeys,
		Expectation{Issuer: settings.Issuer, Audience: serverID, Host: settings.Host}, e.now())
	if err != nil {
		return ExchangeResult{}, err
	}
	if err := ConsumeTicket(ctx, tx, ticket.ID, ticket.ExpiresAt); err != nil {
		return ExchangeResult{}, err
	}
	result := ExchangeResult{Item: ticket.Item, Ref: ticket.Ref}
	// An allowance counts only while free play is on; otherwise the visitor
	// browses like any guest.
	grant := ""
	if settings.FreePlayEnabled && ticket.Grant != "" {
		if err := UpsertGrant(ctx, tx, ticket.Grant, ticket.Play, ticket.GrantExpiresAt); err != nil {
			return ExchangeResult{}, err
		}
		grant, result.FreePlay = ticket.Grant, true
	}

	if in.RefreshToken != "" {
		tokens, err := e.guests.ReissueInTx(auth.WithRequestHost(ctx, in.Host), tx, in.RefreshToken)
		if err == nil {
			// A ticket with an allowance needs a session bound to that grant: a
			// browse-only tab given one gets a new session, and the one it
			// replaces ends now. A plain ticket keeps whatever the tab has.
			var bound string
			if err := tx.QueryRow(ctx, `SELECT COALESCE(play_grant_id, '') FROM bloem_preview_guest_sessions WHERE session_id = $1`, tokens.SessionID).Scan(&bound); err != nil {
				return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
			}
			if grant != "" && bound != grant {
				if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, tokens.SessionID); err != nil {
					return ExchangeResult{}, fmt.Errorf("preview exchange: replacing a session: %w", err)
				}
				tokens = nil
			} else {
				result.FreePlay = bound != "" && settings.FreePlayEnabled
			}
		}
		if err == nil && tokens != nil {
			var expiresAt time.Time
			if err := tx.QueryRow(ctx, `SELECT expires_at FROM auth_sessions WHERE id = $1`, tokens.SessionID).Scan(&expiresAt); err != nil {
				return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
			}
			if err := tx.Commit(ctx); err != nil {
				return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
			}
			result.Tokens, result.ExpiresAt, result.Reused = tokens, expiresAt, true
			return result, nil
		}
		// A stale or foreign token: open a new session instead.
	}

	bucket := e.buckets.Key(in.ClientIP)
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE id IN (
		SELECT s.id `+liveGuests+` AND g.client_bucket = $1
		ORDER BY COALESCE(s.last_seen_at, s.created_at) DESC, s.created_at DESC
		OFFSET $2)`, bucket, MaxGuestsPerClient-1); err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: evicting a client's oldest session: %w", err)
	}
	var live int
	if err := tx.QueryRow(ctx, `SELECT count(*) `+liveGuests).Scan(&live); err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: counting guests: %w", err)
	}
	if live >= settings.MaxConcurrent {
		return ExchangeResult{}, ErrBusy
	}
	expiresAt := e.now().Add(settings.SessionTTL)
	tokens, err := e.guests.CreateInTx(ctx, tx, auth.NewPreviewGuest{
		AccountID: settings.AccountID, ExpiresAt: expiresAt, IdleTTL: settings.IdleTTL, ClientBucket: bucket,
		PlayGrantID: grant,
	})
	if err != nil {
		return ExchangeResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExchangeResult{}, fmt.Errorf("preview exchange: %w", err)
	}
	result.Tokens, result.ExpiresAt = tokens, expiresAt
	return result, nil
}

// maybeSweep deletes expired ticket uses at most once a minute; a failure
// only delays the next attempt.
func (e *Exchanger) maybeSweep(ctx context.Context) {
	e.sweepMu.Lock()
	due := !e.now().Before(e.nextSweep)
	if due {
		e.nextSweep = e.now().Add(time.Minute)
	}
	e.sweepMu.Unlock()
	if due {
		sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_, _ = SweepTicketUses(sweepCtx, e.pool)
	}
}
