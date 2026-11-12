package storagesource

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RequireNoOverlapTx refuses a selected folder when another library already
// reads any of its files from the same source: the same folder, a folder
// inside it, or a folder containing it. Folders nest by logical path, which
// providers report relative to the source root; the source's root entry, or an
// empty path, is the whole source. A per-source transaction lock serializes
// concurrent saves, so call this before writing the selection and keep the
// transaction open until commit.
func RequireNoOverlapTx(ctx context.Context, tx pgx.Tx, sourceKey uuid.UUID, folderID int, directoryID, logicalPath string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('bloem_storage_selection:' || $1::text, 0))`, sourceKey); err != nil {
		return err
	}
	var overlap bool
	err := tx.QueryRow(ctx, `WITH requested AS (
		SELECT CASE WHEN $3=s.root_entry_id THEN '' ELSE btrim($4, '/') END AS path, s.root_entry_id
		FROM bloem_storage_sources s WHERE s.key=$1)
	SELECT EXISTS(
		SELECT 1 FROM requested q, bloem_storage_selected_roots r
		JOIN library_storage_locations l ON l.id=r.location_id
		CROSS JOIN LATERAL (SELECT btrim(r.logical_path, '/') AS path) other
		WHERE l.source_key=$1 AND l.folder_id<>$2 AND r.enabled
		  AND (q.path='' OR other.path='' OR r.directory_id=q.root_entry_id OR r.directory_id=$3
		       OR other.path=q.path OR starts_with(other.path, q.path || '/') OR starts_with(q.path, other.path || '/')))`,
		sourceKey, folderID, directoryID, logicalPath).Scan(&overlap)
	if err != nil {
		return err
	}
	if overlap {
		return ErrSelectionOverlap
	}
	return nil
}

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

// LibrariesByStorage splits every library by whether it reads from a storage
// source: without lists libraries with paths only, with lists each library
// that has at least one storage location, mixed libraries included. Both are
// ordered by library ID.
func (r *Repository) LibrariesByStorage(ctx context.Context) (without, with []int, err error) {
	rows, err := r.pool.Query(ctx, `SELECT f.id, EXISTS(SELECT 1 FROM library_storage_locations l WHERE l.folder_id=f.id) FROM media_folders f ORDER BY f.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	without, with = []int{}, []int{}
	for rows.Next() {
		var id int
		var stored bool
		if err := rows.Scan(&id, &stored); err != nil {
			return nil, nil, err
		}
		if stored {
			with = append(with, id)
		} else {
			without = append(without, id)
		}
	}
	return without, with, rows.Err()
}
