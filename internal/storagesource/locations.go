package storagesource

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FolderLocation returns the library's storage location, if it has one. A
// library has at most one; disabled sources keep their location and never fall
// back to filesystem access.
func (r *Repository) FolderLocation(ctx context.Context, folderID int) (Location, bool, error) {
	if folderID <= 0 {
		return Location{}, false, ErrReferenceConflict
	}
	var location Location
	err := r.pool.QueryRow(ctx, `SELECT id,source_key,folder_id FROM library_storage_locations WHERE folder_id=$1`, folderID).
		Scan(&location.ID, &location.SourceKey, &location.FolderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, false, nil
	}
	if err != nil {
		return Location{}, false, err
	}
	return location, true, nil
}

// FolderLocations returns the storage location of each listed library that has one.
func (r *Repository) FolderLocations(ctx context.Context, folderIDs []int) (map[int]Location, error) {
	out := make(map[int]Location, len(folderIDs))
	if len(folderIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT id,source_key,folder_id FROM library_storage_locations WHERE folder_id=ANY($1)`, folderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.SourceKey, &location.FolderID); err != nil {
			return nil, err
		}
		out[location.FolderID] = location
	}
	return out, rows.Err()
}

// AddLocationTx gives a new library its storage location inside the caller's
// library-creation transaction. A source backs at most one library.
func (r *Repository) AddLocationTx(ctx context.Context, tx pgx.Tx, sourceKey uuid.UUID, folderID int) (Location, error) {
	if tx == nil || sourceKey == uuid.Nil || folderID <= 0 {
		return Location{}, fmt.Errorf("invalid storage location")
	}
	location := Location{ID: uuid.New(), SourceKey: sourceKey, FolderID: folderID}
	_, err := tx.Exec(ctx, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, location.ID, location.SourceKey, location.FolderID)
	if isUniqueViolation(err) {
		return Location{}, ErrSourceInUse
	}
	return location, err
}
