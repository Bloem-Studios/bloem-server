//go:build integration

package storagesource

import (
	"context"
	"errors"
	"testing"
	"time"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

func TestExpiredOwnerCannotCommit(t *testing.T) {
	r, s, old, checkpoint := scanFixture(t)
	if _, err := r.Begin(context.Background(), s.Key, "worker-two", time.Minute); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("active lease stolen: %v", err)
	}
	execSQL(t, r.pool, `UPDATE bloem_storage_scan_runs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, old.RunID)
	if err := r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Complete: true}); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("expired commit: %v", err)
	}
	newer, err := NewRepository(r.pool).Begin(context.Background(), s.Key, "worker-two", time.Minute)
	if err != nil || newer.Epoch <= old.Epoch || newer.RunID != old.RunID {
		t.Fatalf("takeover: %v", err)
	}
	for _, err := range []error{r.Renew(context.Background(), old, time.Minute), r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Complete: true}), r.Complete(context.Background(), old)} {
		if !errors.Is(err, ErrStaleLease) {
			t.Fatalf("stale worker survived: %v", err)
		}
	}
	if err := r.ApplyPage(context.Background(), newer, checkpoint, &storagev1.ListResponse{Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationReplacementRejectsOldGeneration(t *testing.T) {
	r, s, old, checkpoint := scanFixture(t)
	execSQL(t, r.pool, `UPDATE bloem_storage_sources SET root_entry_id='replacement',configuration_revision=2 WHERE key=$1`, s.Key)
	if err := r.ApplyPage(context.Background(), old, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("old")}, Complete: true}); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("old root commit: %v", err)
	}
	fresh, err := r.Begin(context.Background(), s.Key, "worker-two", time.Minute)
	if err != nil || fresh.RunID == old.RunID || fresh.ConfigurationRevision != 2 {
		t.Fatalf("replacement generation: %v", err)
	}
	pending, ok, err := r.NextDirectory(context.Background(), fresh)
	if err != nil || !ok || pending.DirectoryID != "replacement" {
		t.Fatal("mixed roots")
	}
	if err := r.ApplyPage(context.Background(), fresh, pending, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("new")}, Complete: true}); err != nil {
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
