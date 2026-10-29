package preview

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UpsertGrant records a storefront free-play grant the first time a ticket
// names it. A later ticket for the same grant changes nothing: the first
// exchange fixed its allowance and end, and what it has used stays used.
func UpsertGrant(ctx context.Context, tx pgx.Tx, id string, allowance time.Duration, expires time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_preview_play_grants (grant_id, allowance_seconds, expires_at)
		VALUES ($1, $2, $3) ON CONFLICT (grant_id) DO NOTHING`, id, int(allowance/time.Second), expires); err != nil {
		return fmt.Errorf("recording a free-play grant: %w", err)
	}
	return nil
}

// FreePlay is a guest session's live free-play grant.
type FreePlay struct {
	GrantID   string
	Allowance time.Duration
	Used      time.Duration
	ExpiresAt time.Time
}

// LiveGrant reads a guest session's grant while it may still play: the
// preview and free play are on, some of the allowance is left and the grant
// has not ended. Settings are read in the same statement, so switching free
// play off ends it on the next request.
func LiveGrant(ctx context.Context, db queryRower, sessionID string) (FreePlay, bool, error) {
	var out FreePlay
	var allowance int
	var used float64
	err := db.QueryRow(ctx, `SELECT g.grant_id, g.allowance_seconds, g.used_seconds, g.expires_at
		FROM bloem_preview_guest_sessions gs
		JOIN bloem_preview_play_grants g ON g.grant_id = gs.play_grant_id
		JOIN bloem_preview_settings s ON s.enabled AND s.free_play_enabled
		WHERE gs.session_id = $1 AND g.used_seconds < g.allowance_seconds AND g.expires_at > now()`, sessionID).
		Scan(&out.GrantID, &allowance, &used, &out.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return FreePlay{}, false, nil
	}
	if err != nil {
		return FreePlay{}, false, fmt.Errorf("reading a free-play grant: %w", err)
	}
	out.Allowance, out.Used = time.Duration(allowance)*time.Second, time.Duration(used*float64(time.Second))
	return out, true, nil
}

// LiveFreePlay implements the auth middleware's free-play source. It reads
// the database on every call: a security decision never waits for a cache.
func (r *Reader) LiveFreePlay(ctx context.Context, sessionID string) (string, bool, error) {
	grant, live, err := LiveGrant(ctx, r.store.pool, sessionID)
	return grant.GrantID, live, err
}
