package storagesource

import (
	"context"
	"testing"

	"github.com/google/uuid"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// A completed run's per-run rows (directories and, through them, roots,
// listed entries, cursors and staged tracks) are only read while that run is
// in progress. Once a newer run completes they are history; left behind,
// every nightly run adds a full copy of the location's directory tree, and on
// a large location the tables grow until autoanalyze stops keeping up with
// the run in progress. The run rows themselves stay: entries, sources and
// location directories still reference them.
func TestCompletedRunsKeepOnlyTheLatestRunsRowsDB(t *testing.T) {
	r, _, l := scopedFixture(t, "root")
	var calls int
	client := providerClient(t, func(_ context.Context, q *storagev1.ListRequest) (*storagev1.ListResponse, error) {
		if q.DirectoryId == "root" {
			return &storagev1.ListResponse{Entries: []*storagev1.Entry{dirEntry("shelf"), bookEntry("book")}, Complete: true}, nil
		}
		return &storagev1.ListResponse{Entries: []*storagev1.Entry{bookEntry("shelf-book")}, Complete: true}, nil
	})
	first := scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	second := scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))
	latest := scopedRun(t, r, l, client, scopedPublisher(t, l, &calls))

	perRun := func(run uuid.UUID) (dirs, roots, entries, cursors int) {
		t.Helper()
		if err := r.pool.QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM bloem_storage_scan_directories WHERE run_id=$1),
 (SELECT count(*) FROM bloem_storage_scan_directory_roots WHERE run_id=$1),
 (SELECT count(*) FROM bloem_storage_scan_directory_entries WHERE run_id=$1),
 (SELECT count(*) FROM bloem_storage_scan_cursors WHERE run_id=$1)`, run).Scan(&dirs, &roots, &entries, &cursors); err != nil {
			t.Fatal(err)
		}
		return dirs, roots, entries, cursors
	}
	for _, old := range []Lease{first, second} {
		if dirs, roots, entries, cursors := perRun(old.RunID); dirs+roots+entries+cursors != 0 {
			t.Fatalf("older run %s keeps per-run rows: directories=%d roots=%d entries=%d cursors=%d", old.RunID, dirs, roots, entries, cursors)
		}
		var state string
		if err := r.pool.QueryRow(t.Context(), `SELECT state FROM bloem_storage_scan_runs WHERE id=$1`, old.RunID).Scan(&state); err != nil {
			t.Fatalf("older run row is gone: %v", err)
		}
		if state != "complete" {
			t.Fatalf("older run state = %q, want complete", state)
		}
	}
	if dirs, roots, _, _ := perRun(latest.RunID); dirs == 0 || roots == 0 {
		t.Fatalf("latest run lost its rows: directories=%d roots=%d", dirs, roots)
	}
}
