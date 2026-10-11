package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestBeginAuthorizedRequiresAuthority(t *testing.T) {
	r := &Repository{}
	if _, err := r.BeginAuthorized(t.Context(), uuid.New(), "worker", time.Minute, nil); err == nil {
		t.Fatal("missing authority allowed lease creation")
	}
}

// Acquiring journal locks first would wait on this source instead of returning
// the denied authority immediately, inverting native management's lock order.
func TestBeginAuthorizedAuthorizesBeforeJournalLocks(t *testing.T) {
	r, s, _, _ := scanFixture(t)
	blocker, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(t.Context(), "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE", s.Key); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	rejected := errors.New("begin authority revoked")
	if _, err := r.BeginAuthorized(ctx, s.Key, "new-worker", time.Minute, func(context.Context, pgx.Tx) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("journal lock acquired before authority: %v", err)
	}
}

func TestBeginAuthorizedFailureRollsBackTransaction(t *testing.T) {
	for _, failure := range []string{"authority", "active-lease"} {
		t.Run(failure, func(t *testing.T) {
			r, s, prior, _ := scanFixture(t)
			rejected := errors.New("begin refused")
			authorize := func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "UPDATE bloem_storage_sources SET root_entry_id='uncommitted' WHERE key=$1", s.Key); err != nil {
					return err
				}
				if failure == "authority" {
					return rejected
				}
				return nil
			}
			_, err := r.BeginAuthorized(t.Context(), s.Key, "foreign-worker", time.Minute, authorize)
			want := rejected
			if failure == "active-lease" {
				want = ErrStaleLease
			}
			if !errors.Is(err, want) {
				t.Fatalf("begin failure=%s: %v", failure, err)
			}
			var root, owner string
			var epoch int64
			var discovery uuid.UUID
			if err := r.pool.QueryRow(t.Context(), `SELECT s.root_entry_id,s.discovery_run_id,r.owner,r.lease_epoch
				FROM bloem_storage_sources s JOIN bloem_storage_scan_runs r ON r.id=s.discovery_run_id WHERE s.key=$1`, s.Key).Scan(&root, &discovery, &owner, &epoch); err != nil {
				t.Fatal(err)
			}
			if root != "root" || discovery != prior.RunID || owner != prior.Owner || epoch != prior.Epoch {
				t.Fatalf("failed begin committed root=%s discovery=%v owner=%s epoch=%d", root, discovery, owner, epoch)
			}
		})
	}
}
