//go:build integration

package resourcetenancy

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

func TestStoragePageReplayRequiresCurrentAuthority(t *testing.T) {
	for _, revoked := range []string{"library", "organization", "entitlement"} {
		t.Run(revoked, func(t *testing.T) {
			x := newNativeScanFixture(t, "entitled")
			lease, err := x.repo.Begin(t.Context(), x.source.Key, "page-authority", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, pending, err := x.repo.NextDirectory(t.Context(), lease)
			if err != nil || !pending {
				t.Fatal("initial page absent", err)
			}
			page := &storagev1.ListResponse{Entries: []*storagev1.Entry{{Id: "book", Name: "book.epub", Size: 12, Revision: "v1", Kind: storagev1.EntryKind_ENTRY_KIND_FILE}}, NextCursor: "second"}
			authorize := func(ctx context.Context, tx pgx.Tx) error {
				return x.store.RequireStorageScanTx(ctx, tx, x.binding, x.source)
			}
			if err := x.repo.ApplyPageAuthorized(t.Context(), lease, checkpoint, page, authorize, nil); err != nil {
				t.Fatal(err)
			}
			switch revoked {
			case "library":
				nativeScanSQL(t, x.pool, "UPDATE media_folders SET enabled=false WHERE id=$1", x.binding.FolderID)
			case "organization":
				nativeScanSQL(t, x.pool, "UPDATE organizations SET status='suspended' WHERE id=$1", x.org)
			case "entitlement":
				nativeScanSQL(t, x.pool, "UPDATE organization_entitlements SET status='revoked',revoked_at=now() WHERE organization_id=$1 AND plugin_installation_id=$2", x.org, x.source.InstallationID)
			}
			if err := x.repo.ApplyPageAuthorized(t.Context(), lease, checkpoint, page, authorize, nil); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("revoked page replay accepted: %v", err)
			}
			checkpoint.Cursor = "second"
			if err := x.repo.ApplyPageAuthorized(t.Context(), lease, checkpoint, &storagev1.ListResponse{Complete: true}, authorize, nil); !errors.Is(err, ErrResourceHidden) {
				t.Fatalf("revoked next page accepted: %v", err)
			}
			var entries, cursors, processed int
			if err := x.pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1),
				(SELECT count(*) FROM bloem_storage_scan_cursors WHERE run_id=$2),
				(SELECT processed_count FROM bloem_storage_scan_directories WHERE run_id=$2 AND directory_id='root')`,
				x.source.Key, lease.RunID).Scan(&entries, &cursors, &processed); err != nil {
				t.Fatal(err)
			}
			if entries != 1 || cursors != 1 || processed != 1 {
				t.Fatalf("revoked page changed journal: entries=%d cursors=%d processed=%d", entries, cursors, processed)
			}
		})
	}
}

func TestStoragePageAuthorityLocksInstallationBeforeSource(t *testing.T) {
	x := newNativeScanFixture(t, "platform")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	lease, err := x.repo.Begin(ctx, x.source.Key, "lock-order", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, pending, err := x.repo.NextDirectory(ctx, lease)
	if err != nil || !pending {
		t.Fatal("initial page absent", err)
	}
	mutation, err := x.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mutation.Rollback(context.Background()) }()
	var installation int64
	if err := mutation.QueryRow(ctx, "SELECT id FROM plugin_installations WHERE id=$1 FOR UPDATE", x.source.InstallationID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	var mutationPID int
	if err := mutation.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&mutationPID); err != nil {
		t.Fatal(err)
	}
	pid := make(chan int, 1)
	result := make(chan error, 1)
	go func() {
		result <- x.repo.ApplyPageAuthorized(ctx, lease, checkpoint, &storagev1.ListResponse{Complete: true}, func(ctx context.Context, tx pgx.Tx) error {
			var workerPID int
			if err := tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&workerPID); err != nil {
				return err
			}
			pid <- workerPID
			return x.store.RequireStorageScanTx(ctx, tx, x.binding, x.source)
		}, nil)
	}()
	var workerPID int
	select {
	case workerPID = <-pid:
	case err := <-result:
		t.Fatalf("page returned before authority wait: %v", err)
	case <-ctx.Done():
		t.Fatal("page did not reach authority")
	}
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		var blocked bool
		if err := x.pool.QueryRow(ctx, "SELECT $2::integer=ANY(pg_blocking_pids($1))", workerPID, mutationPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-poll.C:
		case err := <-result:
			t.Fatalf("page did not wait for installation: %v", err)
		case <-ctx.Done():
			t.Fatal("installation wait absent")
		}
	}
	// A page that locked its source before checking installation authority
	// would prevent this registry-style mutation from acquiring its next lock.
	var config int64
	if err := mutation.QueryRow(ctx, "SELECT configuration_revision FROM bloem_storage_sources WHERE key=$1 FOR UPDATE NOWAIT", x.source.Key).Scan(&config); err != nil {
		t.Fatalf("page acquired source before installation: %v", err)
	}
	if _, err := mutation.Exec(ctx, "UPDATE plugin_installations SET enabled=false WHERE id=$1", installation); err != nil {
		t.Fatal(err)
	}
	if err := mutation.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrResourceHidden) {
			t.Fatalf("page ignored committed disablement: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("page did not finish after mutation")
	}
}
