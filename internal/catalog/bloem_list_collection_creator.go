package catalog

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/idgen"
)

// BloemCreateListCollectionTx preserves native library ordering and the manual
// server representation while leaving the atomic creation receipt to its caller.
// The caller supplies freshly authorized, enabled tenant library IDs.
func BloemCreateListCollectionTx(ctx context.Context, tx pgx.Tx, name string, libraries []int, binding string) (string, int64, error) {
	libraries = normalizeCollectionLibraryIDs(0, libraries)
	if tx == nil || strings.TrimSpace(name) == "" || len(name) > 200 || len(libraries) == 0 || len(libraries) > 1000 {
		return "", 0, errors.New("invalid list collection definition")
	}
	if _, err := uuid.Parse(binding); err != nil {
		return "", 0, err
	}
	id, err := idgen.NextID()
	if err != nil {
		return "", 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO library_collections(id,library_id,slug,title,collection_type,source_config) VALUES($1,$2,$3,$4,'manual',jsonb_build_object('bloem_list_binding',$3::text))`, id, libraries[0], binding, name); err != nil {
		return "", 0, err
	}
	if err = lockLibraryCollectionOrderRevisions(ctx, tx, libraries); err != nil {
		return "", 0, err
	}
	for _, library := range libraries {
		if _, err = tx.Exec(ctx, `INSERT INTO library_collection_libraries(collection_id,library_id,group_id,sort_order) SELECT $1,$2,NULL,COALESCE((SELECT MAX(sort_order)+1 FROM library_collection_libraries WHERE library_id=$2 AND group_id IS NULL),0)`, id, library); err != nil {
			return "", 0, err
		}
	}
	var revision int64
	err = tx.QueryRow(ctx, `SELECT revision FROM library_collection_revisions WHERE collection_id=$1`, id).Scan(&revision)
	return id, revision, err
}
