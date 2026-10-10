package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

func TestCompleteAuthorizedRequiresAuthority(t *testing.T) {
	r := &Repository{}
	if err := r.CompleteAuthorized(t.Context(), Lease{}, nil, nil); err == nil {
		t.Fatal("missing authority allowed completion")
	}
}

func TestCompleteAuthorizedAuthorizesBeforeJournalLocks(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	if err := r.ApplyPage(t.Context(), lease, checkpoint, &storagev1.ListResponse{Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	blocker, err := r.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(t.Context(), "SELECT key FROM bloem_storage_sources WHERE key=$1 FOR UPDATE", lease.SourceKey); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	rejected := errors.New("completion authority revoked")
	if err := r.CompleteAuthorized(ctx, lease, func(context.Context, pgx.Tx) error { return rejected }, nil); !errors.Is(err, rejected) {
		t.Fatalf("journal lock acquired before authority: %v", err)
	}
}

func TestCompleteAuthorizedFinalizationRollsBack(t *testing.T) {
	for _, failure := range []string{"authority", "finalizer", "lease"} {
		t.Run(failure, func(t *testing.T) {
			r, _, lease, checkpoint := scanFixture(t)
			if err := r.ApplyPage(t.Context(), lease, checkpoint, &storagev1.ListResponse{Complete: true, Entries: []*storagev1.Entry{bookEntry("book")}}, nil); err != nil {
				t.Fatal(err)
			}
			rejected := errors.New("completion refused")
			authorize := func(context.Context, pgx.Tx) error {
				if failure == "authority" {
					return rejected
				}
				return nil
			}
			finalize := func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, "DELETE FROM bloem_storage_entries WHERE source_key=$1", lease.SourceKey); err != nil {
					return err
				}
				if failure == "lease" {
					_, err := tx.Exec(ctx, "UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp() WHERE id=$1", lease.RunID)
					return err
				}
				return rejected
			}
			err := r.CompleteAuthorized(t.Context(), lease, authorize, finalize)
			want := rejected
			if failure == "lease" {
				want = ErrStaleLease
			}
			if !errors.Is(err, want) {
				t.Fatalf("completion failure=%s: %v", failure, err)
			}
			var entries int
			var state string
			if err := r.pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1),state
				FROM bloem_storage_scan_runs WHERE id=$2`, lease.SourceKey, lease.RunID).Scan(&entries, &state); err != nil {
				t.Fatal(err)
			}
			if entries != 1 || state != "running" {
				t.Fatalf("completion failed but committed entries=%d state=%s", entries, state)
			}
		})
	}
}
