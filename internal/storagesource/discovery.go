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
)

// DiscoverPage visits at most one bounded page. The caller owns lease renewal
// and supplies a client from the validated, host-authorized plugin installation.
// Provider failures preserve their status and leave the last checkpoint intact.
// done is true only when all queued directories have committed terminal pages.
func (r *Repository) DiscoverPage(ctx context.Context, lease Lease, client storagev1.StorageProviderClient) (done bool, err error) {
	checkpoint, pending, err := r.NextDirectory(ctx, lease)
	if err != nil {
		return false, err
	}
	if !pending {
		err = r.Complete(ctx, lease)
		return err == nil, err
	}
	var sourceID string
	err = r.pool.QueryRow(ctx, `SELECT provider_source_id FROM bloem_storage_sources WHERE key=$1 AND configuration_revision=$2 AND enabled`, lease.SourceKey, lease.ConfigurationRevision).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrStaleLease
	}
	if err != nil {
		return false, err
	}
	page, err := fetchPage(ctx, client, sourceID, checkpoint)
	if err != nil {
		return false, err
	}
	return false, r.ApplyPage(ctx, lease, checkpoint, page)
}

func fetchPage(ctx context.Context, client storagev1.StorageProviderClient, sourceID string, checkpoint Checkpoint) (*storagev1.ListResponse, error) {
	if client == nil || !validText(sourceID, 1024, true) || !validText(checkpoint.DirectoryID, 1024, true) || !validText(checkpoint.Cursor, 4096, false) || checkpoint.Complete {
		return nil, fmt.Errorf("invalid storage discovery request")
	}
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	page, err := client.List(requestContext, &storagev1.ListRequest{SourceId: sourceID, DirectoryId: checkpoint.DirectoryID, Cursor: checkpoint.Cursor, MaxEntries: 512}, grpc.MaxCallRecvMsgSize(1<<20))
	if err != nil {
		return nil, err
	}
	if err = mediasource.ValidatePage(checkpoint.Cursor, page); err != nil {
		return nil, err
	}
	return page, nil
}
