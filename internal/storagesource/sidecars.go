package storagesource

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sidecar is a text subtitle the series scan attached to a published native
// file (bloem_storage_episode_sidecars). Its PersistedRef pins the entry and
// revision a read must find; LogicalPath is the entry's listed path.
type Sidecar struct {
	MediaFileID int
	PersistedRef
	Name, Format, Language  string
	Forced, HearingImpaired bool
}

// sidecarSelect lists only sidecars in their file's own location whose entry
// the location still holds, unconfirmed absent, at the recorded revision.
const sidecarSelect = `SELECT c.media_file_id,c.location_id,c.entry_id,c.revision,e.logical_path,c.name,c.format,c.language,c.forced,c.hearing_impaired
 FROM bloem_storage_episode_sidecars c
 JOIN bloem_storage_file_refs x ON x.media_file_id=c.media_file_id AND x.location_id=c.location_id
 JOIN bloem_storage_location_entries e ON e.location_id=c.location_id AND e.entry_id=c.entry_id AND e.revision=c.revision AND e.absence_confirmed_at IS NULL
 WHERE c.media_file_id=$1`

func scanSidecar(row pgx.CollectableRow) (Sidecar, error) {
	var s Sidecar
	err := row.Scan(&s.MediaFileID, &s.LocationID, &s.EntryID, &s.Revision, &s.LogicalPath, &s.Name, &s.Format, &s.Language, &s.Forced, &s.HearingImpaired)
	return s, err
}

// FileSidecars lists a file's sidecars in name order, the order playback
// numbers them in. It grants no access: callers authorize the file first.
func (r *Repository) FileSidecars(ctx context.Context, fileID int) ([]Sidecar, error) {
	if fileID <= 0 {
		return nil, ErrReferenceConflict
	}
	rows, err := r.pool.Query(ctx, sidecarSelect+` ORDER BY c.name,c.entry_id`, fileID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanSidecar)
}

// FileSidecar resolves one sidecar of a file by its entry, as the scan
// currently records it. A sidecar that is gone, attached to another file or
// no longer held by its location is ErrReferenceConflict.
func (r *Repository) FileSidecar(ctx context.Context, fileID int, locationID uuid.UUID, entryID string) (Sidecar, error) {
	if fileID <= 0 || locationID == uuid.Nil || !validText(entryID, 1024, true) {
		return Sidecar{}, ErrReferenceConflict
	}
	rows, err := r.pool.Query(ctx, sidecarSelect+` AND c.location_id=$2 AND c.entry_id=$3`, fileID, locationID, entryID)
	if err != nil {
		return Sidecar{}, err
	}
	s, err := pgx.CollectExactlyOneRow(rows, scanSidecar)
	if errors.Is(err, pgx.ErrNoRows) {
		return Sidecar{}, ErrReferenceConflict
	}
	return s, err
}
