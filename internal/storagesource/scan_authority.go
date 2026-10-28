package storagesource

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ValidatePendingPageTx reads the journal's current lease and confirms the
// checkpoint is a pending, reachable directory of the run at its retained
// cursor. A scan may work on several pending directories at once, so it need
// not be the first. Host scan authorizers must acquire installation/source
// authority before this source/run verifier. The transaction must end before
// provider I/O; this grants no viewer access.
func ValidatePendingPageTx(ctx context.Context, tx pgx.Tx, lease Lease, checkpoint Checkpoint) error {
	if tx == nil || checkpoint.Complete {
		return ErrCheckpointConflict
	}
	if err := readLease(ctx, tx, lease); err != nil {
		return err
	}
	var cursor string
	err := tx.QueryRow(ctx, `SELECT current_cursor FROM bloem_storage_scan_directories d WHERE d.run_id=$1 AND d.directory_id=$2 AND NOT d.complete
 AND EXISTS(SELECT 1 FROM bloem_storage_scan_directory_roots dr WHERE dr.run_id=d.run_id AND dr.directory_id=d.directory_id)`, lease.RunID, checkpoint.DirectoryID).Scan(&cursor)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCheckpointConflict
	}
	if err != nil {
		return err
	}
	// The pending directory position must still match its retained journal state.
	if cursor != checkpoint.Cursor {
		return ErrCheckpointConflict
	}
	return nil
}

// pendingDirectoryTx returns the first pending, reachable directory after
// after (all of them for ""), other than those excluded. Roots are probed per
// candidate row: as a join, the planner read every root of the run.
func pendingDirectoryTx(ctx context.Context, tx pgx.Tx, lease Lease, after string, exclude ...string) (Checkpoint, bool, error) {
	var checkpoint Checkpoint
	if err := readLease(ctx, tx, lease); err != nil {
		return checkpoint, false, err
	}
	if exclude == nil {
		exclude = []string{}
	}
	err := tx.QueryRow(ctx, `SELECT d.directory_id,d.current_cursor,d.complete FROM bloem_storage_scan_directories d
 WHERE d.run_id=$1 AND NOT d.complete AND d.directory_id>$3 AND NOT d.directory_id=ANY($2::text[])
 AND (SELECT true FROM bloem_storage_scan_directory_roots dr WHERE dr.run_id=d.run_id AND dr.directory_id=d.directory_id LIMIT 1)
 ORDER BY d.directory_id LIMIT 1`, lease.RunID, exclude, after).Scan(&checkpoint.DirectoryID, &checkpoint.Cursor, &checkpoint.Complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, false, nil
	}
	return checkpoint, err == nil, err
}
