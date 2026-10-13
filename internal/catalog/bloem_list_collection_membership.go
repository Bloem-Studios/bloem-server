package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// BloemReplaceListCollectionItemsTx publishes into an already-bound server
// collection inside the caller's transaction. Host admin and linking-profile
// authority belong to the caller; this adapter also fences the actual source
// marker and current selected libraries under the native parent lock.
func BloemReplaceListCollectionItemsTx(ctx context.Context, tx pgx.Tx, id, binding string, items []string) (int64, error) {
	if tx == nil || binding == "" || len(items) > 10000 {
		return 0, errors.New("invalid list collection publication")
	}
	var kind, source string
	var legacyLibraryID int
	err := tx.QueryRow(ctx, `SELECT collection_type,COALESCE(source_config->>'bloem_list_binding',''),library_id FROM library_collections WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&kind, &source, &legacyLibraryID)
	if err != nil {
		return 0, err
	}
	if kind != "manual" || source != binding {
		return 0, errors.New("list collection binding mismatch")
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT NOT EXISTS (
 SELECT 1 FROM unnest($2::text[]) AS source(item) WHERE item='' OR NOT EXISTS (
  SELECT 1 FROM media_item_libraries mil JOIN media_items mi ON mi.content_id=mil.content_id
  WHERE mil.content_id=source.item AND (
   EXISTS(SELECT 1 FROM library_collection_libraries l WHERE l.collection_id=$1 AND l.library_id=mil.media_folder_id)
   OR (NOT EXISTS(SELECT 1 FROM library_collection_libraries l WHERE l.collection_id=$1) AND mil.media_folder_id=$3)
  )
 ))`, id, items, legacyLibraryID).Scan(&eligible)
	if err != nil {
		return 0, err
	}
	if !eligible {
		return 0, ErrLibraryCollectionItemNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM library_collection_items WHERE collection_id=$1`, id); err != nil {
		return 0, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO library_collection_items(collection_id,media_item_id,position,source_rank)
 SELECT $1,item,row_number() OVER(ORDER BY first_position)-1,first_position-1
 FROM (SELECT item,min(position) AS first_position FROM unnest($2::text[]) WITH ORDINALITY AS source(item,position) GROUP BY item) ordered`, id, items)
	if err != nil {
		return 0, err
	}
	var revision int64
	err = tx.QueryRow(ctx, `UPDATE library_collection_revisions SET revision=revision+1 WHERE collection_id=$1 RETURNING revision`, id).Scan(&revision)
	return revision, err
}
