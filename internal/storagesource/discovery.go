package storagesource

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
func (r *Repository) FetchPage(ctx context.Context, lease Lease, checkpoint *Checkpoint, client storagev1.StorageProviderClient) (*storagev1.ListResponse, error) {
	if checkpoint == nil {
		return nil, fmt.Errorf("invalid storage discovery request")
	}
	var sourceID string
	err := r.pool.QueryRow(ctx, `SELECT provider_source_id FROM bloem_storage_sources WHERE key=$1 AND configuration_revision=$2 AND enabled`, lease.SourceKey, lease.ConfigurationRevision).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaleLease
	}
	if err != nil {
		return nil, err
	}
	if checkpoint.Cursor != "" {
		return fetchPage(ctx, client, sourceID, *checkpoint, "")
	}
	var token string
	err = r.pool.QueryRow(ctx, `SELECT token FROM bloem_storage_change_tokens
		WHERE source_key=$1 AND directory_id=$2 AND configuration_revision=$3 AND issued_at > now() - ($4::bigint * interval '1 second') AND location_id=$5 AND selection_revision=$6`,
		lease.SourceKey, checkpoint.DirectoryID, lease.ConfigurationRevision, int64(maxChangeTokenAge/time.Second), lease.LocationID, lease.SelectionRevision).Scan(&token)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	page, err := fetchPage(ctx, client, sourceID, *checkpoint, token)
	if token != "" && status.Code(err) == codes.FailedPrecondition {
		if _, err := r.pool.Exec(ctx, `DELETE FROM bloem_storage_change_tokens WHERE location_id=$1 AND directory_id=$2 AND configuration_revision=$3 AND selection_revision=$4 AND token=$5`, lease.LocationID, checkpoint.DirectoryID, lease.ConfigurationRevision, lease.SelectionRevision, token); err != nil {
			return nil, err
		}
		token = ""
		page, err = fetchPage(ctx, client, sourceID, *checkpoint, "")
	}
	// Without a change token the provider lists every entry of the directory.
	checkpoint.FullListing = err == nil && token == ""
	return page, err
}

func fetchPage(ctx context.Context, client storagev1.StorageProviderClient, sourceID string, checkpoint Checkpoint, changesSince string) (*storagev1.ListResponse, error) {
	if client == nil || !validText(sourceID, 1024, true) || !validText(checkpoint.DirectoryID, 1024, true) || !validText(checkpoint.Cursor, 4096, false) || checkpoint.Complete {
		return nil, fmt.Errorf("invalid storage discovery request")
	}
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	page, err := client.List(requestContext, &storagev1.ListRequest{SourceId: sourceID, DirectoryId: checkpoint.DirectoryID, Cursor: checkpoint.Cursor, MaxEntries: 512, ChangesSince: changesSince}, grpc.MaxCallRecvMsgSize(1<<20))
	if err != nil {
		return nil, err
	}
	if err = mediasource.ValidatePage(checkpoint.Cursor, page); err != nil {
		return nil, err
	}
	return page, nil
}
