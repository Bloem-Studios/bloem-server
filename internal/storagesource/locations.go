package storagesource

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// FolderLocation is the legacy single-location projection. Ambiguous libraries
// fail closed; callers that support mixed selections use LocationSelections.
func (r *Repository) FolderLocation(ctx context.Context, folderID int) (Location, bool, error) {
	if folderID <= 0 {
		return Location{}, false, ErrReferenceConflict
	}
	locations, err := r.FolderLocations(ctx, []int{folderID})
	location, ok := locations[folderID]
	return location, ok, err
}

// FolderLocations returns the storage location of each listed library that has one.
func (r *Repository) FolderLocations(ctx context.Context, folderIDs []int) (map[int]Location, error) {
	out := make(map[int]Location, len(folderIDs))
	if len(folderIDs) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT id,source_key,folder_id,selection_revision FROM library_storage_locations WHERE folder_id=ANY($1) ORDER BY folder_id,id`, folderIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var location Location
		if err := rows.Scan(&location.ID, &location.SourceKey, &location.FolderID, &location.SelectionRevision); err != nil {
			return nil, err
		}
		if _, exists := out[location.FolderID]; exists {
			return nil, ErrReferenceConflict
		}
		out[location.FolderID] = location
	}
	return out, rows.Err()
}

// AddLocationTx gives a new library its storage location inside the caller's
// library-creation transaction. Each library/source pair retains one location.
func (r *Repository) AddLocationTx(ctx context.Context, tx pgx.Tx, sourceKey uuid.UUID, folderID int) (Location, error) {
	if tx == nil || sourceKey == uuid.Nil || folderID <= 0 {
		return Location{}, fmt.Errorf("invalid storage location")
	}
	location := Location{ID: uuid.New(), SourceKey: sourceKey, FolderID: folderID, SelectionRevision: 1}
	_, err := tx.Exec(ctx, `INSERT INTO library_storage_locations(id,source_key,folder_id) VALUES($1,$2,$3)`, location.ID, location.SourceKey, location.FolderID)
	if isUniqueViolation(err) {
		return Location{}, ErrSourceInUse
	}
	if err != nil {
		return Location{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_storage_selected_roots(id,location_id,directory_id,name,logical_path)
	 SELECT $1,$2,root_entry_id,'','' FROM bloem_storage_sources WHERE key=$3`, uuid.New(), location.ID, sourceKey)
	if err != nil {
		return Location{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO bloem_library_location_state(folder_id) VALUES($1) ON CONFLICT(folder_id) DO NOTHING`, folderID)
	return location, err
}
