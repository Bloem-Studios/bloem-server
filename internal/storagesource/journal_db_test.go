//go:build integration

package storagesource

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	storagev1 "github.com/Bloem-Studios/bloem-plugin-sdk/pkg/pluginproto/bloem/plugin/v1"
)

func bookEntry(id string) *storagev1.Entry {
	return &storagev1.Entry{Id: id, Name: id + ".epub", LogicalPath: "Books/" + id + ".epub", Kind: storagev1.EntryKind_ENTRY_KIND_FILE, Size: 12, Revision: "v1"}
}

func scanFixture(t *testing.T) (*Repository, SourceConfig, Lease, Checkpoint) {
	t.Helper()
	pool := testDatabase(t, true)
	s, r := fixtureSource(t, pool)
	lease, err := r.Begin(context.Background(), s.Key, "worker-one", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, ok, err := r.NextDirectory(context.Background(), lease)
	if err != nil || !ok || checkpoint.DirectoryID != "root" {
		t.Fatalf("initial checkpoint: %v", err)
	}
	return r, s, lease, checkpoint
}

func TestApplyPageRollsBackCheckpointAndEntries(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	execSQL(t, r.pool, `ALTER TABLE bloem_storage_scan_directories ADD CONSTRAINT test_owned_late_failure CHECK(processed_count=0)`)
	child := bookEntry("child")
	child.Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
	page := &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book"), child}, NextCursor: "second"}
	if err := r.ApplyPage(context.Background(), lease, checkpoint, page); err == nil {
		t.Fatal("late SQL failure absent")
	}
	var entries, dirs, cursors int
	if err := r.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bloem_storage_entries),(SELECT count(*) FROM bloem_storage_scan_directories),(SELECT count(*) FROM bloem_storage_scan_cursors)`).Scan(&entries, &dirs, &cursors); err != nil {
		t.Fatal(err)
	}
	got, ok, err := r.NextDirectory(context.Background(), lease)
	if err != nil || !ok || got != checkpoint || entries != 0 || dirs != 1 || cursors != 0 {
		t.Fatalf("partial transaction: %d/%d/%d %v", entries, dirs, cursors, err)
	}
}

func TestPageReplayIsIdempotent(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	page := &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, NextCursor: "second"}
	for range 2 {
		if err := r.ApplyPage(context.Background(), lease, checkpoint, page); err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := r.pool.QueryRow(context.Background(), `SELECT processed_count FROM bloem_storage_scan_directories WHERE run_id=$1`, lease.RunID).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated entries")
	}
	changed := &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("different")}, NextCursor: "second"}
	if err := r.ApplyPage(context.Background(), lease, checkpoint, changed); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	wrong := checkpoint
	wrong.Cursor = "uncommitted"
	if err := r.ApplyPage(context.Background(), lease, wrong, &storagev1.ListResponse{Complete: true}); !errors.Is(err, ErrCheckpointConflict) {
		t.Fatalf("wrong checkpoint accepted: %v", err)
	}
}

func TestHistoricalCursorLoopFails(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	long := strings.Repeat("a", 4096)
	for _, next := range []string{long, "third"} {
		if err := r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{NextCursor: next}); err != nil {
			t.Fatal(err)
		}
		checkpoint.Cursor = next
	}
	if err := r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{NextCursor: long}); err == nil {
		t.Fatal("historical cursor loop accepted")
	}
	got, _, err := r.NextDirectory(context.Background(), lease)
	if err != nil || got.Cursor != "third" {
		t.Fatal("loop advanced checkpoint")
	}
}

func TestIncompleteDirectoriesPreventCompletion(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	if err := r.Complete(context.Background(), lease); err == nil {
		t.Fatal("incomplete root completed")
	}
	child := bookEntry("child")
	child.Kind = storagev1.EntryKind_ENTRY_KIND_DIRECTORY
	if err := r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{child}, Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), lease); err == nil {
		t.Fatal("incomplete child completed")
	}
	pending, ok, err := r.NextDirectory(context.Background(), lease)
	if err != nil || !ok || pending.DirectoryID != "child" {
		t.Fatal("child not queued")
	}
	if err := r.ApplyPage(context.Background(), lease, pending, &storagev1.ListResponse{Complete: true}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateIdentityAcrossPagesRejected(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	if err := r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, NextCursor: "second"}); err != nil {
		t.Fatal(err)
	}
	checkpoint.Cursor = "second"
	if err := r.ApplyPage(context.Background(), lease, checkpoint, &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("book")}, Complete: true}); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	got, _, err := r.NextDirectory(context.Background(), lease)
	if err != nil || got.Complete || got.Cursor != "second" {
		t.Fatal("duplicate advanced checkpoint")
	}
}
