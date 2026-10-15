package preview_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/preview"
)

func consume(t *testing.T, pool *pgxpool.Pool, id string, expires time.Time) error {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := preview.ConsumeTicket(ctx, tx, id, expires); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func TestTicketUseSurvivesANewStore(t *testing.T) {
	ctx, pool := previewTestDB(t)
	id := "replay" + time.Now().Format("150405.000000000")
	if err := consume(t, pool, id, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A new pool stands in for a restarted server: the use is in Postgres.
	again, err := pgxpool.New(ctx, pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := consume(t, again, id, time.Now().Add(time.Minute)); !errors.Is(err, preview.ErrTicketInvalid) {
		t.Fatalf("replay err = %v, want ErrTicketInvalid", err)
	}
}

func TestSweepDropsOnlyExpiredUses(t *testing.T) {
	ctx, pool := previewTestDB(t)
	if err := consume(t, pool, "expired-use-"+time.Now().Format("150405.000000000"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	live := "live-use-" + time.Now().Format("150405.000000000")
	if err := consume(t, pool, live, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := preview.SweepTicketUses(ctx, pool); err != nil || n < 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bloem_preview_ticket_uses WHERE jti=$1`, live).Scan(&left); err != nil || left != 1 {
		t.Fatalf("live use swept: %d (%v)", left, err)
	}
}
