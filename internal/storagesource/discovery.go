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

// FetchPage lists the next page of a pending directory checkpoint from the
// source's provider. The caller applies it with ApplyPage.
func (r *Repository) FetchPage(ctx context.Context, lease Lease, checkpoint Checkpoint, client storagev1.StorageProviderClient) (*storagev1.ListResponse, error) {
	var sourceID string
	err := r.pool.QueryRow(ctx, `SELECT provider_source_id FROM bloem_storage_sources WHERE key=$1 AND configuration_revision=$2 AND enabled`, lease.SourceKey, lease.ConfigurationRevision).Scan(&sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrStaleLease
	}
	if err != nil {
		return nil, err
	}
	return fetchPage(ctx, client, sourceID, checkpoint)
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
