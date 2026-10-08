package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// UpsertBatchTx upserts items inside the caller's transaction as one
// PostgreSQL batch and queues their search-index events. Storage library
// scans publish a whole listing page this way instead of one round trip per item.
func (r *ItemRepository) UpsertBatchTx(ctx context.Context, tx pgx.Tx, items []*models.MediaItem) error {
	if tx == nil {
		return fmt.Errorf("media item batch upsert: nil transaction")
	}
	if len(items) == 0 {
		return nil
	}
	capture := &itemBatchCapture{batch: &pgx.Batch{}}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if _, err := r.writeItem(ctx, capture, item, true); err != nil {
			return err
		}
		ids = append(ids, item.ContentID)
	}
	results := tx.SendBatch(ctx, capture.batch)
	for range capture.batch.Len() {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("media item batch upsert: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("media item batch upsert: %w", err)
	}
	return r.searchIndexEvents.EnqueueUpserts(ctx, tx, ids)
}

// itemBatchCapture queues writeItem's item statement instead of executing it.
// The per-item search event writeItem also issues is dropped here: the batch
// enqueues every item's event in one statement after the upserts.
type itemBatchCapture struct{ batch *pgx.Batch }

func (c *itemBatchCapture) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(query, "catalog_search_index_events") {
		c.batch.Queue(query, args...)
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
