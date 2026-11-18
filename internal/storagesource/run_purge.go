package storagesource

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// runPurgeBatch bounds each purge statement. Directory rows cascade to their
// roots, listed entries, cursors and staged tracks, so one batch removes a few
// times this many rows; small batches keep each delete short and never hold a
// scan's locks.
const runPurgeBatch = 2000

// purgeFinishedRunRows deletes the per-run rows of the completed runs in the
// same scope as keep (the run that just completed): the location's runs, or
// for a source-level run the source's. Those rows are only read while their
// run is in progress. Left in place, every completed run keeps a full copy of
// the location's directory tree, and on a large location the tables grow
// until autoanalyze no longer keeps up with the run in progress. The run rows
// themselves stay, because entries, sources and location directories
// reference them.
func (r *Repository) purgeFinishedRunRows(ctx context.Context, lease Lease) error {
	scope := `r.location_id=$1`
	scopeID := lease.LocationID
	if lease.LocationID == uuid.Nil {
		scope = `r.location_id IS NULL AND r.source_key=$1`
		scopeID = lease.SourceKey
	}
	var finished []uuid.UUID
	rows, err := r.pool.Query(ctx, `SELECT r.id FROM bloem_storage_scan_runs r WHERE `+scope+` AND r.state='complete' AND r.id<>$2`, scopeID, lease.RunID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		finished = append(finished, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(finished) == 0 {
		return err
	}
	purged := int64(0)
	for {
		tag, err := r.pool.Exec(ctx, `DELETE FROM bloem_storage_scan_directories d
 USING (SELECT run_id,directory_id FROM bloem_storage_scan_directories WHERE run_id=ANY($1::uuid[]) LIMIT $2) old
 WHERE d.run_id=old.run_id AND d.directory_id=old.directory_id`, finished, runPurgeBatch)
		if err != nil {
			return err
		}
		purged += tag.RowsAffected()
		if tag.RowsAffected() == 0 {
			break
		}
		if err = ctx.Err(); err != nil {
			return err
		}
	}
	if purged > 0 {
		slog.InfoContext(ctx, "storage scan: purged finished runs' directory rows", "component", "storagesource",
			"run_id", lease.RunID, "finished_runs", len(finished), "directories", purged)
	}
	return nil
}
