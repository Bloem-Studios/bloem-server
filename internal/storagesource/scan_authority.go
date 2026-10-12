package storagesource

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ValidatePendingPageTx retains the journal's current lease and next pending
// directory checkpoint through the caller's transaction. Host scan authorizers
// must acquire installation/source authority before this source/run verifier.
// The transaction must end before provider I/O; this grants no viewer access.
func ValidatePendingPageTx(ctx context.Context, tx pgx.Tx, lease Lease, checkpoint Checkpoint) error {
	if tx == nil || checkpoint.Complete {
		return ErrCheckpointConflict
	}
	current, pending, err := pendingDirectoryTx(ctx, tx, lease)
	if err != nil {
		return err
	}
	if !pending || current != checkpoint {
		return ErrCheckpointConflict
	}
	return nil
}

func pendingDirectoryTx(ctx context.Context, tx pgx.Tx, lease Lease) (Checkpoint, bool, error) {
	var checkpoint Checkpoint
	if err := verifyLease(ctx, tx, lease); err != nil {
		return checkpoint, false, err
	}
	err := tx.QueryRow(ctx, `SELECT directory_id,current_cursor,complete FROM bloem_storage_scan_directories WHERE run_id=$1 AND NOT complete AND EXISTS(SELECT 1 FROM bloem_storage_scan_directory_roots dr WHERE dr.run_id=bloem_storage_scan_directories.run_id AND dr.directory_id=bloem_storage_scan_directories.directory_id) ORDER BY directory_id LIMIT 1`, lease.RunID).Scan(&checkpoint.DirectoryID, &checkpoint.Cursor, &checkpoint.Complete)
	if errors.Is(err, pgx.ErrNoRows) {
		return checkpoint, false, nil
	}
	return checkpoint, err == nil, err
}
