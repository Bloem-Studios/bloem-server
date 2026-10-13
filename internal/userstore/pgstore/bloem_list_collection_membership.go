package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// BloemReplaceListCollectionItemsTx is a database-only publication adapter. The
// caller checks current profile/catalog authority and the binding generation,
// then commits membership, binding state and receipt in this same transaction.
// It cannot convert or write an ordinary manual collection.
func BloemReplaceListCollectionItemsTx(ctx context.Context, tx pgx.Tx, user int, profile, id, binding string, items []string) (int64, error) {
	if tx == nil || user <= 0 || profile == "" || binding == "" || len(items) > 10000 {
		return 0, errors.New("invalid list collection publication")
	}
	var owner, kind, source string
	err := tx.QueryRow(ctx, `SELECT creator_profile_id,collection_type,COALESCE(source_config->>'bloem_list_binding','') FROM user_personal_collections WHERE user_id=$1 AND id=$2 AND native FOR UPDATE`, user, id).Scan(&owner, &kind, &source)
	if err != nil {
		return 0, err
	}
	if owner != profile || kind != "synced" || source != binding {
		return 0, errors.New("list collection binding mismatch")
	}
	unique := make(map[string]bool, len(items))
	for _, item := range items {
		if item == "" {
			return 0, errors.New("invalid list collection item")
		}
		unique[item] = true
	}
	// Native personal lists intentionally allow external entries. This source
	// publication contains only resolved catalog rows; do not fabricate entries
	// if a matched row disappeared, and retain their keys through publication.
	var found int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT content_id FROM media_items WHERE content_id=ANY($1::text[]) FOR KEY SHARE) catalog_rows`, items).Scan(&found); err != nil {
		return 0, err
	}
	if found != len(unique) {
		return 0, errors.New("list collection catalog item missing")
	}
	if _, err = tx.Exec(ctx, `DELETE FROM user_personal_collection_items WHERE user_id=$1 AND collection_id=$2`, user, id); err != nil {
		return 0, err
	}
	// First occurrence wins, and dense positions preserve source order.
	_, err = tx.Exec(ctx, `INSERT INTO user_personal_collection_items(user_id,collection_id,media_item_id,position,added_at)
 SELECT $1,$2,item,row_number() OVER(ORDER BY first_position)-1,clock_timestamp()
 FROM (SELECT item,min(position) AS first_position FROM unnest($3::text[]) WITH ORDINALITY AS source(item,position) GROUP BY item) ordered`, user, id, items)
	if err != nil {
		return 0, err
	}
	// Empty-to-empty publication must consume a native witness as well. Advance
	// only after parent/items locks, matching ordinary revision trigger ordering.
	var revision int64
	err = tx.QueryRow(ctx, `UPDATE user_collection_revisions SET revision=revision+1 WHERE user_id=$1 AND collection_id=$2 RETURNING revision`, user, id).Scan(&revision)
	return revision, err
}
