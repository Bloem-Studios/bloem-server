package downloads

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBloemQuotaLockUsesOneConnection(t *testing.T) {
	fixture := seedManagedFixture(t)
	cfg := fixture.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := NewRepository(pool)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var escaped context.Context
	err = repo.WithUserQuotaLock(ctx, fixture.userID, func(ctx context.Context) error {
		escaped = ctx
		_, err := repo.CountActiveByUser(ctx, fixture.userID)
		return err
	})
	if err != nil {
		t.Fatalf("quota callback must not reacquire its only pool connection: %v", err)
	}
	if pool.Stat().AcquiredConns() != 0 {
		t.Fatal("quota lock retained its connection")
	}
	if _, err := repo.CountActiveByUser(escaped, fixture.userID); err == nil {
		t.Fatal("ended quota context borrowed a returned connection")
	}
	// Cleanup must use a fresh context even when the request is cancelled.
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	marker := errors.New("callback failure")
	err = repo.WithUserQuotaLock(ctx, fixture.userID, func(context.Context) error {
		cancel()
		return marker
	})
	if !errors.Is(err, marker) {
		t.Fatalf("callback failure was lost: %v", err)
	}
	check, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	// A different pool/session must acquire it; re-entering the same session
	// would conceal a leaked advisory lock because session locks are reentrant.
	other := NewRepository(fixture.pool)
	if err := other.WithUserQuotaLock(check, fixture.userID, func(ctx context.Context) error {
		_, err := other.CountActiveByUser(ctx, fixture.userID)
		return err
	}); err != nil {
		t.Fatalf("cancelled request retained the quota lock: %v", err)
	}
}
