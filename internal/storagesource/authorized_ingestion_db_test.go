//go:build integration

package storagesource

// PRE-MODE repository/protocol/migration controls only. Callback stand-ins and
// trusted file/ref writes here supply no CURRENT native publication authority.
// Legal current publication coverage belongs to the B-backed consumer fixture.

import (
	"context"
	"errors"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func TestAuthorizedIngestionRequiresAuthorizerAndRollsBack(t *testing.T) {
	r, _, _, lease, claim := claimFixture(t)
	ctx := context.Background()
	called := false
	publish := func(context.Context, pgx.Tx, *storagev1.Entry) error { called = true; return nil }
	if err := r.PublishAuthorizedIngestion(ctx, claim, nil, publish); !errors.Is(err, ErrIngestionAuthorizationRequired) || called {
		t.Fatalf("nil authorization reached publication: %v", err)
	}
	denied := errors.New("authority denied")
	if err := r.PublishAuthorizedIngestion(ctx, claim, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('authorization-rollback','ebook','Denied')"); err != nil {
			return err
		}
		return denied
	}, publish); !errors.Is(err, denied) || called {
		t.Fatalf("denied publication: %v", err)
	}
	var count int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE content_id='authorization-rollback'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("authority writes survived rollback: %d %v", count, err)
	}
	late := errors.New("late publication failure")
	if err := r.PublishAuthorizedIngestion(ctx, claim, func(context.Context, pgx.Tx) error { return nil }, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
		if _, err := tx.Exec(ctx, "INSERT INTO media_items(content_id,type,title) VALUES('authorization-rollback','ebook','Late')"); err != nil {
			return err
		}
		return late
	}); !errors.Is(err, late) {
		t.Fatalf("late failure: %v", err)
	}
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE content_id='authorization-rollback'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("publisher writes survived rollback: %d %v", count, err)
	}
	again, ok, err := r.NextIngestion(ctx, lease)
	if err != nil || !ok || again.Token != claim.Token {
		t.Fatalf("rollback changed checkpoint: %v", err)
	}
	if err = r.PublishAuthorizedIngestion(ctx, claim, func(context.Context, pgx.Tx) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	next, ok, err := r.NextIngestion(ctx, lease)
	if err != nil || !ok || next.Entry.GetId() != "b" {
		t.Fatalf("authorized skip did not advance: %v", err)
	}
}

func TestAuthorizedIngestionPreservesFullClaimAndLeaseChecks(t *testing.T) {
	for _, mode := range []string{"token", "entry", "epoch", "owner", "expired", "generation", "config", "disabled", "binding", "late-expiry"} {
		t.Run(mode, func(t *testing.T) {
			r, s, _, l, c := claimFixture(t)
			c.Entry = proto.CloneOf(c.Entry)
			switch mode {
			case "token":
				c.Token = uuid.New()
			case "entry":
				c.Entry.Name = "untrusted name"
			case "epoch":
				c.Lease.Epoch++
			case "owner":
				c.Lease.Owner = "foreign"
			case "expired":
				execSQL(t, r.pool, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second'")
			case "generation":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET discovery_run_id=NULL WHERE key=$1", s.Key)
			case "config":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET configuration_revision=2 WHERE key=$1", s.Key)
			case "disabled":
				execSQL(t, r.pool, "UPDATE bloem_storage_sources SET enabled=false WHERE key=$1", s.Key)
			case "binding":
				c.Lease.BindingID = uuid.New()
			}
			published := false
			err := r.PublishAuthorizedIngestion(t.Context(), c, func(context.Context, pgx.Tx) error { return nil }, func(ctx context.Context, tx pgx.Tx, _ *storagev1.Entry) error {
				published = true
				if mode == "late-expiry" {
					_, err := tx.Exec(ctx, "UPDATE bloem_storage_ingestion SET lease_until=clock_timestamp()-interval '1 second' WHERE run_id=$1 AND binding_id=$2", l.RunID, l.BindingID)
					return err
				}
				return nil
			})
			if err == nil || (published && mode != "late-expiry") {
				t.Fatalf("invalid claim published: %v", err)
			}
			if mode == "late-expiry" {
				again, ok, err := r.NextIngestion(t.Context(), l)
				if err != nil || !ok || again.Token != c.Token {
					t.Fatalf("late expiry advanced checkpoint: %v", err)
				}
			}
		})
	}
}

func TestAuthorizedIngestionAuthorizesBeforeSourceLock(t *testing.T) {
	r, s, _, _, claim := claimFixture(t)
	// The authorizer must be able to take the source lock on another connection.
	// This would fail with NOWAIT if lease verification had already run.
	err := r.PublishAuthorizedIngestion(t.Context(), claim, func(ctx context.Context, _ pgx.Tx) error {
		tx, err := r.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		_, err = tx.Exec(ctx, "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE NOWAIT", s.Key)
		return err
	}, nil)
	if err != nil {
		t.Fatalf("authorizer ran after source lock: %v", err)
	}
}
