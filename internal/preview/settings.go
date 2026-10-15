// Package preview runs guest preview sessions: the settings record, ticket
// verification, single-use ticket ids and the exchange that opens a guest
// session on the shared preview account.
package preview

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/auth"
)

// ErrInvalidSettings wraps every settings validation failure.
var ErrInvalidSettings = errors.New("preview: invalid settings")

// settingsLockKey serializes exchanges with settings writes, so a kill switch
// cannot interleave with an exchange that is opening a session.
const settingsLockKey int64 = 0x626c6f656d707276 // "bloemprv"

// Settings is the preview configuration (spec §6.5).
type Settings struct {
	Enabled          bool
	Host             string
	Issuer           string
	AccountID        int
	TicketPublicKeys []ed25519.PublicKey
	SessionTTL       time.Duration
	IdleTTL          time.Duration
	MaxConcurrent    int
	Brand            string
	CTAURL           string
	CTALabel         string
	StorefrontURL    string
	LaunchURL        string
	MaturityCeiling  string
}

// Store reads and writes the single settings row.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

const settingsColumns = `enabled, host, issuer, COALESCE(account_id, 0), ticket_public_keys,
	session_ttl_seconds, idle_ttl_seconds, max_concurrent, brand, cta_url, cta_label,
	storefront_url, launch_url, maturity_ceiling`

func (s *Store) Get(ctx context.Context) (Settings, error) { return getSettings(ctx, s.pool) }

func getSettings(ctx context.Context, db queryRower) (Settings, error) {
	var out Settings
	var keys []string
	var sessionTTL, idleTTL int
	err := db.QueryRow(ctx, `SELECT `+settingsColumns+` FROM bloem_preview_settings`).Scan(
		&out.Enabled, &out.Host, &out.Issuer, &out.AccountID, &keys, &sessionTTL, &idleTTL,
		&out.MaxConcurrent, &out.Brand, &out.CTAURL, &out.CTALabel, &out.StorefrontURL,
		&out.LaunchURL, &out.MaturityCeiling)
	if err != nil {
		return Settings{}, fmt.Errorf("reading preview settings: %w", err)
	}
	out.SessionTTL = time.Duration(sessionTTL) * time.Second
	out.IdleTTL = time.Duration(idleTTL) * time.Second
	for _, encoded := range keys {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return Settings{}, fmt.Errorf("reading preview settings: stored ticket key is not an Ed25519 public key")
		}
		out.TicketPublicKeys = append(out.TicketPublicKeys, ed25519.PublicKey(raw))
	}
	return out, nil
}

var hostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

func validate(s Settings) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidSettings}, args...)...)
	}
	if s.Host != "" && !hostPattern.MatchString(s.Host) {
		return fail("host must be a lower-case DNS name without scheme, port or path")
	}
	if len(s.TicketPublicKeys) > 2 {
		return fail("at most two ticket keys")
	}
	for _, key := range s.TicketPublicKeys {
		if len(key) != ed25519.PublicKeySize {
			return fail("ticket keys must be Ed25519 public keys")
		}
	}
	if len(s.TicketPublicKeys) == 2 && bytes.Equal(s.TicketPublicKeys[0], s.TicketPublicKeys[1]) {
		return fail("ticket keys must be distinct")
	}
	if s.MaturityCeiling != "" {
		if _, ok := access.AgeForCeiling(s.MaturityCeiling); !ok {
			return fail("maturity ceiling is not a recognized content rating")
		}
	}
	if len(s.Issuer) > 128 {
		return fail("issuer is too long")
	}
	if s.Enabled && (s.Host == "" || s.Issuer == "" || s.AccountID <= 0 || len(s.TicketPublicKeys) == 0) {
		return fail("an enabled preview needs a host, an issuer, an account and a ticket key")
	}
	if s.SessionTTL < 5*time.Minute || s.SessionTTL > 24*time.Hour {
		return fail("session lifetime must be between 5 minutes and 24 hours")
	}
	if s.IdleTTL < time.Minute || s.IdleTTL > 2*time.Hour || s.IdleTTL > s.SessionTTL {
		return fail("idle lifetime must be between one minute and two hours, and no longer than the session")
	}
	if s.MaxConcurrent < 1 || s.MaxConcurrent > 10000 {
		return fail("capacity must be between 1 and 10000")
	}
	for name, value := range map[string]string{"brand": s.Brand, "cta_label": s.CTALabel, "maturity_ceiling": s.MaturityCeiling} {
		if len(value) > 120 {
			return fail("%s is too long", name)
		}
	}
	for name, value := range map[string]string{"cta_url": s.CTAURL, "storefront_url": s.StorefrontURL, "launch_url": s.LaunchURL} {
		if len(value) > 2048 {
			return fail("%s is too long", name)
		}
		if value != "" {
			u, err := url.Parse(value)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return fail("%s must be an absolute HTTPS URL without credentials", name)
			}
		}
	}
	return nil
}

// Put replaces the settings. Making an account the shared preview account
// revokes its sessions and deletes its API keys in the same transaction, so
// the database guard (migration 20261014130000) holds from then on. Turning
// the preview off revokes every guest.
func (s *Store) Put(ctx context.Context, next Settings) error {
	if err := validate(next); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("writing preview settings: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.putInTransaction(ctx, tx, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) putInTransaction(ctx context.Context, tx pgx.Tx, next Settings) error {
	if err := validate(next); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, settingsLockKey); err != nil {
		return fmt.Errorf("writing preview settings: %w", err)
	}
	previous, err := getSettings(ctx, tx)
	if err != nil {
		return err
	}
	if previous.Issuer != "" && next.Issuer != previous.Issuer {
		return fmt.Errorf("%w: the preview belongs to another issuer", ErrInvalidSettings)
	}
	if next.AccountID > 0 {
		var role string
		err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id = $1 FOR UPDATE`, next.AccountID).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: the preview account does not exist", ErrInvalidSettings)
		}
		if err != nil {
			return fmt.Errorf("writing preview settings: %w", err)
		}
		if role == "admin" {
			return fmt.Errorf("%w: an administrator cannot be the preview account", ErrInvalidSettings)
		}
		if next.AccountID != previous.AccountID {
			if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, next.AccountID); err != nil {
				return fmt.Errorf("revoking the preview account's sessions: %w", err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM api_keys WHERE user_id = $1`, next.AccountID); err != nil {
				return fmt.Errorf("deleting the preview account's API keys: %w", err)
			}
		}
	}
	if previous.AccountID > 0 && (!next.Enabled || next.AccountID != previous.AccountID) {
		if err := revokeGuests(ctx, tx); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(next.TicketPublicKeys))
	for _, key := range next.TicketPublicKeys {
		keys = append(keys, base64.StdEncoding.EncodeToString(key))
	}
	var account any
	if next.AccountID > 0 {
		account = next.AccountID
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=$1, host=$2, issuer=$3, account_id=$4,
		ticket_public_keys=$5, session_ttl_seconds=$6, idle_ttl_seconds=$7, max_concurrent=$8, brand=$9,
		cta_url=$10, cta_label=$11, storefront_url=$12, launch_url=$13, maturity_ceiling=$14, updated_at=now()`,
		next.Enabled, next.Host, next.Issuer, account, keys, int(next.SessionTTL/time.Second),
		int(next.IdleTTL/time.Second), next.MaxConcurrent, next.Brand, next.CTAURL, next.CTALabel,
		next.StorefrontURL, next.LaunchURL, next.MaturityCeiling); err != nil {
		return fmt.Errorf("writing preview settings: %w", err)
	}
	return nil
}

// Disable is the kill switch: the preview turns off and every guest session
// ends in the same transaction.
func (s *Store) Disable(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("disabling the preview: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, settingsLockKey); err != nil {
		return fmt.Errorf("disabling the preview: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_preview_settings SET enabled=false, updated_at=now()`); err != nil {
		return fmt.Errorf("disabling the preview: %w", err)
	}
	if err := revokeGuests(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func revokeGuests(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = now()
		WHERE auth_method = 'preview_guest' AND revoked_at IS NULL`); err != nil {
		return fmt.Errorf("revoking guest sessions: %w", err)
	}
	return nil
}

// Reader caches presentation settings. Guest admission reads authoritative
// settings so host/account changes never wait for the cache expiry.
type Reader struct {
	store *Store
	ttl   time.Duration
	now   func() time.Time

	mu      sync.Mutex
	cached  Settings
	loaded  time.Time
	present bool
}

func NewReader(store *Store, ttl time.Duration) *Reader {
	return &Reader{store: store, ttl: ttl, now: time.Now}
}

// Settings returns the cached settings, reloading after ttl. A failed reload
// returns the error; callers decide whether to fail closed.
func (r *Reader) Settings(ctx context.Context) (Settings, error) {
	r.mu.Lock()
	if r.present && r.now().Sub(r.loaded) < r.ttl {
		cached := r.cached
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()
	fresh, err := r.store.Get(ctx)
	if err != nil {
		return Settings{}, err
	}
	r.mu.Lock()
	r.cached, r.loaded, r.present = fresh, r.now(), true
	r.mu.Unlock()
	return fresh, nil
}

// Invalidate drops the cached settings.
func (r *Reader) Invalidate() {
	r.mu.Lock()
	r.present = false
	r.mu.Unlock()
}

// PreviewGuestSettings implements auth.PreviewGuestSettingsSource.
func (r *Reader) PreviewGuestSettings(ctx context.Context) (auth.PreviewGuestSettings, error) {
	// Security decisions must not wait for the presentation cache to expire.
	fresh, err := r.store.Get(ctx)
	if err != nil {
		return auth.PreviewGuestSettings{}, err
	}
	r.mu.Lock()
	r.cached, r.loaded, r.present = fresh, r.now(), true
	r.mu.Unlock()
	return auth.PreviewGuestSettings{Enabled: fresh.Enabled, Host: fresh.Host, AccountID: fresh.AccountID}, nil
}

// CachedAccountID never performs I/O; the host guard refreshes this snapshot
// before the nonblocking activity writer needs it.
func (r *Reader) CachedAccountID() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cached.AccountID
}
