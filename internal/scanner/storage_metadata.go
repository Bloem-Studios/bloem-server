package scanner

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// storageMetadataOwnedTx permits provider metadata only for unlocked items
// without a present local version. Publication may still attach native files
// and membership to curated items without replacing their metadata.
func storageMetadataOwnedTx(ctx context.Context, tx pgx.Tx, contentIDs []string) (map[string]bool, error) {
	owned := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		owned[id] = true
	}
	// Lock first, then use a separate READ COMMITTED statement. A local
	// publisher may commit a present version while this lock is waiting.
	rows, err := tx.Query(ctx, `SELECT content_id FROM media_items WHERE content_id=ANY($1::text[]) ORDER BY content_id FOR UPDATE`, contentIDs)
	if err != nil {
		return nil, err
	}
	if _, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `SELECT mi.content_id, COALESCE(cardinality(mi.locked_fields),0)=0 AND NOT EXISTS (
 SELECT 1 FROM media_files mf WHERE mf.content_id=mi.content_id AND mf.missing_since IS NULL
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_refs native WHERE native.media_file_id=mf.id))
 FROM media_items mi WHERE mi.content_id=ANY($1::text[]) ORDER BY mi.content_id`, contentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var allowed bool
		if err := rows.Scan(&id, &allowed); err != nil {
			return nil, err
		}
		owned[id] = allowed
	}
	return owned, rows.Err()
}
