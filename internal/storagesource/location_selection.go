package storagesource

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SelectedRoot retains a library's selected opaque directory identity even
// when disabled. These rows describe scope; they grant no resource authority.
type SelectedRoot struct {
	ID, LocationID                 uuid.UUID
	DirectoryID, Name, LogicalPath string
	Enabled                        bool
}

type LocationSelection struct {
	Location Location
	Roots    []SelectedRoot
}

// LocationSelections reads all retained plugin selections. Filesystem paths
// stay in media_folder_paths and are projected by the management service.
func (r *Repository) LocationSelections(ctx context.Context, folderID int) ([]LocationSelection, error) {
	if folderID <= 0 {
		return nil, ErrReferenceConflict
	}
	rows, err := r.pool.Query(ctx, `SELECT l.id,l.source_key,l.folder_id,l.selection_revision,
	 r.id,r.directory_id,r.name,r.logical_path,r.enabled
	 FROM library_storage_locations l LEFT JOIN bloem_storage_selected_roots r ON r.location_id=l.id
	 WHERE l.folder_id=$1 ORDER BY l.id,r.directory_id`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LocationSelection, 0)
	for rows.Next() {
		var location Location
		var rootID *uuid.UUID
		var directoryID, name, path *string
		var enabled *bool
		if err := rows.Scan(&location.ID, &location.SourceKey, &location.FolderID, &location.SelectionRevision, &rootID, &directoryID, &name, &path, &enabled); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Location.ID != location.ID {
			out = append(out, LocationSelection{Location: location, Roots: make([]SelectedRoot, 0)})
		}
		if rootID != nil {
			out[len(out)-1].Roots = append(out[len(out)-1].Roots, SelectedRoot{ID: *rootID, LocationID: location.ID, DirectoryID: *directoryID, Name: *name, LogicalPath: *path, Enabled: *enabled})
		}
	}
	return out, rows.Err()
}

// LibraryLocationRevision reads the Bloem-owned aggregate revision. A library
// created through Silo after the migration starts at revision one; its first
// Bloem attachment/edit persists that state inside the write transaction.
// This value alone is not an authenticated edit token for filesystem paths.
func (r *Repository) LibraryLocationRevision(ctx context.Context, folderID int) (int64, error) {
	if folderID <= 0 {
		return 0, ErrReferenceConflict
	}
	var revision int64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(s.revision,1) FROM media_folders f
	 LEFT JOIN bloem_library_location_state s ON s.folder_id=f.id WHERE f.id=$1`, folderID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrReferenceConflict
	}
	return revision, err
}
