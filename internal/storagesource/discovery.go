package storagesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/storageplugin"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
)

// maxChangeTokenAge bounds incremental listing: a directory whose change token
// is older is listed in full, which also reconciles anything a provider's
// change feed missed.
const maxChangeTokenAge = 7 * 24 * time.Hour

// FetchPage lists the next page of a pending directory checkpoint from the
// source's provider. A directory's first page asks for only the changes since
// its last complete listing, when the source issued a change token; a
// provider that cannot honor the token gets a full listing instead. The caller
// applies the page with ApplyPage, passing the checkpoint FetchPage marked.
func (r *Repository) FetchPage(ctx context.Context, lease Lease, checkpoint Checkpoint, client storagev1.StorageProviderClient) (*storagev1.ListResponse, error) {
	var sourceID string
	err := r.pool.QueryRow(ctx, `SELECT provider_source_id FROM bloem_storage_sources WHERE key=$1 AND configuration_revision=$2 AND enabled`, lease.SourceKey, lease.ConfigurationRevision).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaleLease
	}
	if err != nil {
		return nil, err
	}

	mode, token, err := r.directoryListing(ctx, lease, checkpoint)
	if err != nil {
		return nil, err
	}
	page, err := fetchPage(ctx, client, sourceID, checkpoint, token)
	if mode == "incremental" && status.Code(err) == codes.FailedPrecondition {
		if err = r.resetDirectoryFull(ctx, lease, checkpoint); err != nil {
			return nil, err
		}
		if checkpoint.Cursor != "" {
			return nil, ErrCheckpointRestart
		}
		return fetchPage(ctx, client, sourceID, checkpoint, "")
	}

	return page, err
}

// FetchFollowingPage lists the page after checkpoint's, at next, as FetchPage
// will once checkpoint's page is applied, so a scan can list it meanwhile. It
// claims nothing and changes no journal state: the page is used only if the
// journal then stands at next, and ApplyPage checks that cursor again.
func (r *Repository) FetchFollowingPage(ctx context.Context, lease Lease, checkpoint Checkpoint, next string, client storagev1.StorageProviderClient) (*storagev1.ListResponse, error) {
	var sourceID, token string
	err := r.pool.QueryRow(ctx, `SELECT s.provider_source_id,d.changes_since FROM bloem_storage_sources s
 JOIN bloem_storage_scan_directories d ON d.run_id=$3 AND d.directory_id=$4
 WHERE s.key=$1 AND s.configuration_revision=$2 AND s.enabled AND NOT d.complete AND d.current_cursor IN ($5,$6) AND d.listing_mode IN ('full','incremental')`,
		lease.SourceKey, lease.ConfigurationRevision, lease.RunID, checkpoint.DirectoryID, checkpoint.Cursor, next).Scan(&sourceID, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCheckpointConflict
	}
	if err != nil {
		return nil, err
	}
	return fetchPage(ctx, client, sourceID, Checkpoint{DirectoryID: checkpoint.DirectoryID, Cursor: next}, token)
}

func fetchPage(ctx context.Context, client storagev1.StorageProviderClient, sourceID string, checkpoint Checkpoint, changesSince string) (*storagev1.ListResponse, error) {
	if client == nil || !validText(sourceID, 1024, true) || !validText(checkpoint.DirectoryID, 1024, true) || !validText(checkpoint.Cursor, 4096, false) || checkpoint.Complete {
		return nil, fmt.Errorf("invalid storage discovery request")
	}
	// A provider may fill one page from several backend requests, so the page
	// gets longer than the storage session's default RPC deadline.
	const pageTimeout = 30 * time.Second
	requestContext, cancel := context.WithTimeout(ctx, pageTimeout)
	defer cancel()
	page, err := client.List(requestContext, &storagev1.ListRequest{SourceId: sourceID, DirectoryId: checkpoint.DirectoryID, Cursor: checkpoint.Cursor, MaxEntries: 512, ChangesSince: changesSince}, grpc.MaxCallRecvMsgSize(1<<20), storageplugin.RPCLimit(pageTimeout))
	if err != nil {
		return nil, err
	}
	if err = mediasource.ValidatePage(checkpoint.Cursor, page); err != nil {
		return nil, err
	}
	return page, nil
}

// The input token and effective mode are retained before provider I/O, so a
// resumed cursor never guesses whether omission means absence.
func (r *Repository) directoryListing(ctx context.Context, lease Lease, cp Checkpoint) (string, string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = verifyLease(ctx, tx, lease); err != nil {
		return "", "", err
	}
	var mode, token, cursor string
	var complete bool
	err = tx.QueryRow(ctx, `SELECT listing_mode,changes_since,current_cursor,complete FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id=$2 FOR UPDATE`, lease.RunID, cp.DirectoryID).Scan(&mode, &token, &cursor, &complete)
	if err != nil {
		return "", "", err
	}
	if complete || cursor != cp.Cursor {
		return "", "", ErrCheckpointConflict
	}
	if mode == "pending" {
		err = tx.QueryRow(ctx, `SELECT t.token FROM bloem_storage_change_tokens t JOIN bloem_storage_location_directories d ON d.location_id=t.location_id AND d.directory_id=t.directory_id
   WHERE t.source_key=$1 AND t.location_id=$2 AND t.directory_id=$3 AND t.configuration_revision=$4 AND t.selection_revision=$5 AND t.issued_at>now()-($6::bigint*interval '1 second')
   AND d.configuration_revision=$4 AND d.selection_revision=$5 AND d.last_listing_mode IS NOT NULL`, lease.SourceKey, lease.LocationID, cp.DirectoryID, lease.ConfigurationRevision, lease.SelectionRevision, int64(maxChangeTokenAge/time.Second)).Scan(&token)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", "", err
		}
		mode = "full"
		if token != "" {
			mode = "incremental"
		}
		if _, err = tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET listing_mode=$3,changes_since=$4 WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID, mode, token); err != nil {
			return "", "", err
		}
	}
	return mode, token, tx.Commit(ctx)
}
func (r *Repository) resetDirectoryFull(ctx context.Context, lease Lease, cp Checkpoint) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err = verifyLease(ctx, tx, lease); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET listing_mode='full',changes_since='',current_cursor='',complete=false,processed_count=0 WHERE run_id=$1 AND directory_id=$2 AND current_cursor=$3 AND NOT complete`, lease.RunID, cp.DirectoryID, cp.Cursor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrCheckpointConflict
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_storage_scan_cursors WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_storage_scan_directory_entries WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID); err != nil {
		return err
	}
	// The directory's edges no longer count until the full listing observes
	// them again, so directories reached only through them leave the reach.
	if err = rebuildLocationReachTx(ctx, tx, lease); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM bloem_storage_change_tokens WHERE location_id=$1 AND directory_id=$2 AND configuration_revision=$3 AND selection_revision=$4`, lease.LocationID, cp.DirectoryID, lease.ConfigurationRevision, lease.SelectionRevision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
