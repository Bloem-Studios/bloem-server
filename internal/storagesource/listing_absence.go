package storagesource

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FullListings are what a completed run's listings say about absence. A
// directory listed completely without a change token lists the entries its
// provider held while the listing ran, but it is not a point-in-time snapshot:
// a provider may leave an entry updated during the listing to its next one,
// as Bookwarehouse does. So an entry the full listings omit is only suspected
// absent, and a later completed run that does not list it either confirms it.
// A full listing that returned nothing is suspect, as an empty filesystem root
// is: it neither raises nor confirms a suspicion.
type FullListings struct {
	RunID uuid.UUID
	// Directories were listed completely and in full, with at least one entry.
	Directories []string
	// Everything is set when every directory of the run was. Then any entry
	// the run did not list is suspect, including entries of directories no
	// longer listed and entries journaled before directories were recorded.
	Everything bool
	// Listed are the run's directories, except those whose full listing
	// returned no entries. A suspect entry of one of them is confirmed gone.
	Listed []string
	// EmptyFullListing is set when a full listing of the run returned no
	// entries. Then only entries of Listed directories are confirmed gone.
	EmptyFullListing bool
}

// FullListingsTx reports the run's full listings. Call it only in the run's
// completion transaction, after every directory has completed: incremental,
// partial, cancelled and failed listings raise no suspicion.
func FullListingsTx(ctx context.Context, tx pgx.Tx, lease Lease) (FullListings, error) {
	listed := FullListings{RunID: lease.RunID}
	err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(directory_id ORDER BY directory_id) FILTER (WHERE complete AND full_listing AND processed_count>0),'{}'),
 COALESCE(bool_and(complete AND full_listing AND processed_count>0),false),
 COALESCE(array_agg(directory_id ORDER BY directory_id) FILTER (WHERE complete AND NOT (full_listing AND processed_count=0)),'{}'),
 COALESCE(bool_or(complete AND full_listing AND processed_count=0),false)
 FROM bloem_storage_scan_directories WHERE run_id=$1`, lease.RunID).Scan(&listed.Directories, &listed.Everything, &listed.Listed, &listed.EmptyFullListing)
	return listed, err
}
