package storagesource

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FullListings are what a completed run's listings prove absent. A directory
// listed completely without a change token lists every entry it holds, so its
// former entries this run did not list are gone. A full listing that returned
// nothing is suspect, as an empty filesystem root is, and proves nothing.
type FullListings struct {
	RunID uuid.UUID
	// Directories were listed completely and in full, with at least one entry.
	Directories []string
	// Everything is set when every directory of the run was. Then any entry
	// the run did not list is gone, including entries of directories no
	// longer listed and entries journaled before directories were recorded.
	Everything bool
}

// FullListingsTx reports the run's full listings. Call it only in the run's
// completion transaction, after every directory has completed: incremental,
// partial, cancelled and failed listings prove nothing.
func FullListingsTx(ctx context.Context, tx pgx.Tx, lease Lease) (FullListings, error) {
	listed := FullListings{RunID: lease.RunID}
	err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(directory_id ORDER BY directory_id) FILTER (WHERE complete AND full_listing AND processed_count>0),'{}'),
 COALESCE(bool_and(complete AND full_listing AND processed_count>0),false)
 FROM bloem_storage_scan_directories WHERE run_id=$1`, lease.RunID).Scan(&listed.Directories, &listed.Everything)
	return listed, err
}
