// Package pgsourcegate owns the PostgreSQL account gate used by first playback
// admission and legacy launch/persistence. A lease is bound to one pool/account;
// it cannot authorize a selected-source write or survive its callback lifetime.
package pgsourcegate

import (
	"context"
	"sync"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type leaseKey struct{}
type lease struct {
	mu      sync.RWMutex
	active  bool
	pool    *pgxpool.Pool
	account int
}

// Shared holds either the transaction's own shared gate or a reference to the
// enclosing launch lease. The returned release must run after this operation's
// transaction has committed or rolled back. Joining avoids deadlock when first
// admission is queued behind the launch while its SaveAttempt uses another pool
// connection. Asynchronous use after launch release obtains its own gate.
func Shared(ctx context.Context, tx pgx.Tx, pool *pgxpool.Pool, account int) (func(), error) {
	if held, ok := ctx.Value(leaseKey{}).(*lease); ok && held.pool == pool && held.account == account {
		held.mu.RLock()
		if held.active {
			return held.mu.RUnlock, nil
		}
		held.mu.RUnlock()
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended('playback-source:'||$1::integer::text,0))`, account)
	return func() {}, err
}

// Legacy holds the gate across the complete legacy launch/reconstruction, with
// no source/attempt row locks. Bound initial activation must not call this. An
// admitted account refuses even if its source has subsequently been blocked.
func Legacy(ctx context.Context, pool *pgxpool.Pool, account int) (context.Context, func(), error) {
	// Nested reconstruction stays inside the existing lease; do not pin
	// another connection merely to recheck a marker that cannot change yet.
	if held, ok := ctx.Value(leaseKey{}).(*lease); ok && held.pool == pool && held.account == account {
		held.mu.RLock()
		if held.active {
			var once sync.Once
			return ctx, func() { once.Do(held.mu.RUnlock) }, nil
		}
		held.mu.RUnlock()
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return ctx, nil, err
	}
	rollback := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}
	release, err := Shared(ctx, tx, pool, account)
	if err != nil {
		rollback()
		return ctx, nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_markers WHERE user_id=$1) OR EXISTS(SELECT 1 FROM playback_source_registrations WHERE user_id=$1)`, account).Scan(&exists); err != nil {
		rollback()
		release()
		return ctx, nil, err
	}
	if exists {
		rollback()
		release()
		return ctx, nil, userstore.ErrPlaybackSourceUnbound
	}
	held := &lease{active: true, pool: pool, account: account}
	var once sync.Once
	done := func() {
		once.Do(func() { held.mu.Lock(); held.active = false; rollback(); release(); held.mu.Unlock() })
	}
	return context.WithValue(ctx, leaseKey{}, held), done, nil
}
