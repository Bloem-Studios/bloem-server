package storagesource

import (
	"context"
	"testing"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

// An ignored folder (.recyclebin, @eaDir, ...) is recorded but never listed:
// the scan completes without queueing it, so its files cost no provider calls.
func TestApplyPageSkipsIgnoredDirectories(t *testing.T) {
	r, _, lease, checkpoint := scanFixture(t)
	directory := func(id, logical string) *storagev1.Entry {
		return &storagev1.Entry{Id: id, Name: logical, LogicalPath: logical, Kind: storagev1.EntryKind_ENTRY_KIND_DIRECTORY, Revision: "dir"}
	}
	page := &storagev1.ListResponse{Entries: []*storagev1.Entry{directory("recycle", ".recyclebin"), directory("title", "3 A.M. (2001) {imdb-tt0248661} {tmdb-77084}")}, Complete: true}
	skip := func(entry *storagev1.Entry) bool { return entry.GetName() == ".recyclebin" }
	authorize := func(context.Context, pgx.Tx) error { return nil }
	for range 2 {
		if err := r.ApplyPageAuthorizedSkipping(context.Background(), lease, checkpoint, page, skip, authorize, nil); err != nil {
			t.Fatal(err)
		}
	}
	var entries int
	var queued []string
	if err := r.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM bloem_storage_entries WHERE source_key=$1),
		(SELECT array_agg(directory_id ORDER BY directory_id) FROM bloem_storage_scan_directories WHERE run_id=$2)`, lease.SourceKey, lease.RunID).Scan(&entries, &queued); err != nil {
		t.Fatal(err)
	}
	if entries != 2 || len(queued) != 2 || queued[0] != "root" || queued[1] != "title" {
		t.Fatalf("entries=%d queued=%v", entries, queued)
	}
	pending, ok, err := r.NextDirectory(context.Background(), lease)
	if err != nil || !ok || pending.DirectoryID != "title" {
		t.Fatalf("next directory %+v %v %v", pending, ok, err)
	}
	if err := r.ApplyPageAuthorizedSkipping(context.Background(), lease, pending, &storagev1.ListResponse{Complete: true}, skip, authorize, nil); err != nil {
		t.Fatal(err)
	}
	if _, more, err := r.NextDirectory(context.Background(), lease); err != nil || more {
		t.Fatalf("skipped directory still pending: more=%v err=%v", more, err)
	}
	if err := r.Complete(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
}
