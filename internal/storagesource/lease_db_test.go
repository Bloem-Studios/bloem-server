package storagesource

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestExpiredOwnerCannotCommit(t *testing.T) {
	r, s, old, checkpoint := scanFixture(t)
	if _, err := r.Begin(context.Background(), s.Key, "worker-two", time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("active lease stolen: %v", err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.RunID)
	if err := r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Complete: true}, nil); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("expired commit: %v", err)
	}
	newer, err := NewRepository(r.pool).Begin(context.Background(), s.Key, "worker-two", time.Minute)
	if err != nil || newer.Epoch <= old.Epoch || newer.RunID != old.RunID {
		t.Fatalf("takeover: %v", err)
	}
	for _, err := range []error{r.Renew(context.Background(), old, time.Minute), r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Complete: true}, nil), r.Complete(context.Background(), old)} {
		if !errors.Is(err, ErrStaleLease) {
			t.Fatalf("stale worker survived: %v", err)
		}
	}
	if err := r.ApplyPage(context.Background(), newer, checkpoint, &storagev1.ListResponse{Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationReplacementRejectsOldGeneration(t *testing.T) {
	r, s, old, checkpoint := scanFixture(t)
	execSQL(t, r.pool, `UPDATE bloem_storage_sources SET root_entry_id='replacement',configuration_revision=2 WHERE key=$1`, s.Key)
	if err := r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("old")}, Complete: true}, nil); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("old root commit: %v", err)
	}
	// Configured-root replacement is accompanied by a new retained selection;
	// discovery never substitutes source configuration for selected directories.
	execSQL(t, r.pool, `UPDATE bloem_storage_selected_roots SET directory_id='replacement' WHERE location_id=$1`, old.LocationID)
	execSQL(t, r.pool, `UPDATE library_storage_locations SET selection_revision=selection_revision+1 WHERE id=$1`, old.LocationID)
	fresh, err := r.Begin(context.Background(), s.Key, "worker-two", time.Minute)
	if err != nil || fresh.RunID == old.RunID || fresh.ConfigurationRevision != 2 {
		t.Fatalf("replacement generation: %v", err)
	}
	pending, ok, err := r.NextDirectory(context.Background(), fresh)
	if err != nil || !ok || pending.DirectoryID != "replacement" {
		t.Fatal("mixed roots")
	}
	if err := r.ApplyPage(context.Background(), fresh, pending, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("new")}, Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM bloem_storage_entries`).Scan(&count); err != nil || count != 1 {
		t.Fatal("old root leaked into new generation")
	}
}

func TestRenewAndDisableFence(t *testing.T) {
	r, s, lease, _ := scanFixture(t)
	if err := r.Renew(context.Background(), lease, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := r.Renew(context.Background(), lease, 0); err == nil {
		t.Fatal("zero lease accepted")
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_sources SET enabled=false WHERE key=$1`, s.Key)
	if _, _, err := r.NextDirectory(context.Background(), lease); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("disabled scan continues: %v", err)
	}
	if _, err := r.Begin(context.Background(), s.Key, "worker", time.Minute); !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("disabled begin: %v", err)
	}
}

// Library scan authorizers retain every source of the library FOR SHARE before
// the journal verifies its lease; another library sharing the source holds the
// same SHARE. Journal operations must accept that retained mode rather than
// upgrade it (an upgrade closes a 40P01 cycle), while still fencing source
// configuration writes until they commit.
func TestJournalOperationsShareRetainedSourceLockDB(t *testing.T) {
	r, s, lease, checkpoint := scanFixture(t)
	retain := func() pgx.Tx {
		t.Helper()
		holder, err := r.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		// Release the connection before the pool closes, including on failure.
		t.Cleanup(func() { _ = holder.Rollback(context.Background()) })
		if _, err = holder.Exec(t.Context(), `SELECT key FROM bloem_storage_sources WHERE key=$1 FOR SHARE`, s.Key); err != nil {
			t.Fatal(err)
		}
		return holder
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	bounded := func() (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 2*time.Second) }

	holder := retain()
	op, stop := bounded()
	if _, _, err := r.NextDirectory(op, lease); err != nil {
		t.Fatalf("next directory upgraded retained source lock: %v", err)
	}
	stop()
	op, stop = bounded()
	if err := r.Renew(op, lease, time.Minute); err != nil {
		t.Fatalf("renewal upgraded retained source lock: %v", err)
	}
	stop()
	op, stop = bounded()
	tx, err := r.pool.Begin(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = ValidatePendingPageTx(op, tx, lease, checkpoint); err != nil {
		t.Fatalf("scan-entry validation upgraded retained source lock: %v", err)
	}
	if err = tx.Commit(op); err != nil {
		t.Fatal(err)
	}
	stop()
	op, stop = bounded()
	if err = r.ApplyPage(op, lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("one")}, NextCursor: "next"}, nil); err != nil {
		t.Fatalf("page upgraded retained source lock: %v", err)
	}
	stop()
	if err = holder.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}

	// The page's own source lock still excludes a configuration writer.
	fenced := false
	publish := func(ctx context.Context, _ pgx.Tx, _ []*storagev1.Entry, _ []string) error {
		writer, err := r.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = writer.Rollback(context.Background()) }()
		_, err = writer.Exec(ctx, `SELECT key FROM bloem_storage_sources WHERE key=$1 FOR NO KEY UPDATE NOWAIT`, s.Key)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			return fmt.Errorf("configuration writer not fenced by page: %v", err)
		}
		fenced = true
		return nil
	}
	next := Checkpoint{DirectoryID: checkpoint.DirectoryID, Cursor: "next"}
	if err = r.ApplyPage(ctx, lease, next, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("two")}, Complete: true}, publish); err != nil || !fenced {
		t.Fatalf("fenced page: fenced=%v err=%v", fenced, err)
	}

	holder = retain()
	op, stop = bounded()
	defer stop()
	if err = r.Complete(op, lease); err != nil {
		t.Fatalf("completion upgraded retained source lock: %v", err)
	}
}

// A scan that stops without completing must not keep its claim: a retry within
// the lease TTL would fail as a stale lease. Release is fenced to its epoch.
func TestReleaseLetsRetryResumeAtOnce(t *testing.T) {
	ctx := context.Background()
	r, s, old, checkpoint := scanFixture(t)
	if _, err := r.Begin(ctx, s.Key, "worker-two", time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("active lease stolen: %v", err)
	}
	if err := r.Release(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := r.Renew(ctx, old, time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("released claim renewed: %v", err)
	}
	newer, err := r.Begin(ctx, s.Key, "worker-two", time.Minute)
	if err != nil || newer.RunID != old.RunID || newer.Epoch != old.Epoch+1 {
		t.Fatalf("retry did not resume the released run: %+v %v", newer, err)
	}
	// A late release by the earlier claim must not end the newer one.
	if err := r.Release(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := r.Renew(ctx, newer, time.Minute); err != nil {
		t.Fatalf("stale release ended the newer claim: %v", err)
	}
	if err := r.ApplyPage(ctx, newer, checkpoint, &storagev1.ListResponse{Complete: true}, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := r.Release(ctx, newer); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := r.pool.QueryRow(ctx, `SELECT state FROM bloem_storage_scan_runs WHERE id=$1`, newer.RunID).Scan(&state); err != nil || state != "complete" {
		t.Fatalf("release changed a completed run: state=%s %v", state, err)
	}
	invalid := newer
	invalid.Owner = ""
	if err := r.Release(ctx, invalid); err == nil {
		t.Fatal("ownerless release accepted")
	}
}
