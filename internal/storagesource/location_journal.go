package storagesource

import (
	"context"
	"errors"

	storagev1 "github.com/Silo-Server/silo-server/internal/storageproto/bloem/plugin/v1"
	"github.com/jackc/pgx/v5"
)

// Directory evidence is separate from source staging: aliases may share an
// entry identity, but a provider cannot repeat one ID across a directory's pages.
func applyLocationEntries(ctx context.Context, tx pgx.Tx, lease Lease, cp Checkpoint, page *storagev1.ListResponse, skip DirectorySkipper) ([]*storagev1.Entry, error) {
	var mode string
	if err := tx.QueryRow(ctx, `SELECT listing_mode FROM bloem_storage_scan_directories WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID).Scan(&mode); err != nil {
		return nil, err
	}
	if mode == "pending" {
		mode = "full"
		if _, err := tx.Exec(ctx, `UPDATE bloem_storage_scan_directories SET listing_mode='full' WHERE run_id=$1 AND directory_id=$2`, lease.RunID, cp.DirectoryID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_location_directories(location_id,directory_id,configuration_revision,selection_revision) VALUES($1,$2,$3,$4) ON CONFLICT(location_id,directory_id) DO UPDATE SET configuration_revision=EXCLUDED.configuration_revision,selection_revision=EXCLUDED.selection_revision`, lease.LocationID, cp.DirectoryID, lease.ConfigurationRevision, lease.SelectionRevision); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(page.Entries)+len(page.RemovedEntryIds))
	removed := make([]bool, 0, cap(ids))
	for _, e := range page.Entries {
		ids = append(ids, e.Id)
		removed = append(removed, false)
	}
	for _, id := range page.RemovedEntryIds {
		ids = append(ids, id)
		removed = append(removed, true)
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_scan_directory_entries(run_id,directory_id,entry_id,removed) SELECT $1,$2,x.id,x.removed FROM unnest($3::text[],$4::bool[]) x(id,removed)`, lease.RunID, cp.DirectoryID, ids, removed); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrReferenceConflict
			}
			return nil, err
		}
	}
	files := make([]*storagev1.Entry, 0, len(page.Entries))
	for _, e := range page.Entries {
		var oldKind int32
		var oldSize, oldModified int64
		var oldRevision string
		err := tx.QueryRow(ctx, `SELECT kind,size,modified_unix_nano,revision FROM bloem_storage_location_entries WHERE location_id=$1 AND entry_id=$2 AND last_seen_run=$3`, lease.LocationID, e.Id, lease.RunID).Scan(&oldKind, &oldSize, &oldModified, &oldRevision)
		if err == nil {
			if oldKind != int32(e.Kind) || oldSize != e.Size || oldModified != e.ModifiedUnixNano || oldRevision != e.Revision {
				return nil, ErrReferenceConflict
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		} else {
			if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_location_entries(location_id,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,selection_revision,last_seen_run)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(location_id,entry_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,kind=EXCLUDED.kind,size=EXCLUDED.size,modified_unix_nano=EXCLUDED.modified_unix_nano,revision=EXCLUDED.revision,configuration_revision=EXCLUDED.configuration_revision,selection_revision=EXCLUDED.selection_revision,last_seen_run=EXCLUDED.last_seen_run,absence_confirmed_at=NULL`, lease.LocationID, e.Id, e.Name, e.LogicalPath, int32(e.Kind), e.Size, e.ModifiedUnixNano, e.Revision, lease.ConfigurationRevision, lease.SelectionRevision, lease.RunID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_entries(source_key,entry_id,name,logical_path,kind,size,modified_unix_nano,revision,configuration_revision,last_seen_run)
    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(source_key,entry_id) DO UPDATE SET name=EXCLUDED.name,logical_path=EXCLUDED.logical_path,kind=EXCLUDED.kind,size=EXCLUDED.size,modified_unix_nano=EXCLUDED.modified_unix_nano,revision=EXCLUDED.revision,configuration_revision=EXCLUDED.configuration_revision,last_seen_run=EXCLUDED.last_seen_run,absence_confirmed_at=NULL`, lease.SourceKey, e.Id, e.Name, e.LogicalPath, int32(e.Kind), e.Size, e.ModifiedUnixNano, e.Revision, lease.ConfigurationRevision, lease.RunID); err != nil {
				return nil, err
			}
			if e.Kind == storagev1.EntryKind_ENTRY_KIND_FILE {
				files = append(files, e)
			}
		}
		if e.Kind == storagev1.EntryKind_ENTRY_KIND_DIRECTORY && skip != nil && skip(e) {
			if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges WHERE location_id=$1 AND parent_directory_id=$2 AND entry_id=$3`, lease.LocationID, cp.DirectoryID, e.Id); err != nil {
				return nil, err
			}
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO bloem_storage_location_edges(location_id,parent_directory_id,entry_id) VALUES($1,$2,$3) ON CONFLICT(location_id,parent_directory_id,entry_id) DO UPDATE SET absence_suspected_run=NULL`, lease.LocationID, cp.DirectoryID, e.Id); err != nil {
			return nil, err
		}
	}
	if len(page.RemovedEntryIds) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges WHERE location_id=$1 AND parent_directory_id=$2 AND entry_id=ANY($3::text[])`, lease.LocationID, cp.DirectoryID, page.RemovedEntryIds); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// UNION (not UNION ALL) terminates cycles and carries each root across aliases,
// including aliases discovered after the target directory finished. Edges from
// a pending full traversal cannot prove descent until its first page commits.
const locationReachSQL = `WITH RECURSIVE reach(directory_id,root_id) AS (
 SELECT directory_id,id FROM bloem_storage_selected_roots WHERE location_id=$2 AND enabled
 UNION
 SELECT e.entry_id,r.root_id FROM reach r
 JOIN bloem_storage_scan_directories d ON d.run_id=$1 AND d.directory_id=r.directory_id AND d.listing_mode<>'pending'
 JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=r.directory_id AND e.absence_suspected_run IS NULL
 AND (d.listing_mode<>'full' OR EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries observed WHERE observed.run_id=$1 AND observed.directory_id=r.directory_id AND observed.entry_id=e.entry_id AND NOT observed.removed))
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id AND x.kind=2
 WHERE x.configuration_revision=$3
) `

func propagateLocationRootsTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	args := []any{lease.RunID, lease.LocationID, lease.ConfigurationRevision}
	if _, err := tx.Exec(ctx, locationReachSQL+`INSERT INTO bloem_storage_scan_directories(run_id,directory_id) SELECT DISTINCT $1,directory_id FROM reach ON CONFLICT DO NOTHING`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_scan_directory_roots WHERE run_id=$1`, lease.RunID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, locationReachSQL+`INSERT INTO bloem_storage_scan_directory_roots(run_id,directory_id,location_id,root_id) SELECT $1,directory_id,$2,root_id FROM reach`, args...); err != nil {
		return err
	}
	// Only positive evidence is published before successful completion. The
	// completing graph replaces provisional/legacy memberships atomically.
	_, err := tx.Exec(ctx, `INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id)
  SELECT DISTINCT $2,f.media_file_id,r.root_id FROM bloem_storage_scan_directory_roots r
  JOIN bloem_storage_scan_directories d ON d.run_id=r.run_id AND d.directory_id=r.directory_id AND d.listing_mode<>'pending'
  JOIN bloem_storage_location_edges e ON e.location_id=$2 AND e.parent_directory_id=r.directory_id
  JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
  JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
  WHERE r.run_id=$1 AND f.configuration_revision=$3
  ON CONFLICT(location_id,media_file_id,root_id) DO UPDATE SET enabled=true`, args...)
	return err
}

// Completion reconciles only this location's retained directory edges. A full
// listing can defer present entries, so its omissions need a later completed
// listing to confirm. Positive observations clear suspicion in the page transaction.
const completedLocationReachSQL = `WITH RECURSIVE reach(directory_id,root_id) AS (
 SELECT directory_id,id FROM bloem_storage_selected_roots WHERE location_id=$1 AND enabled
 UNION
 SELECT e.entry_id,r.root_id FROM reach r
 JOIN bloem_storage_location_edges e ON e.location_id=$1 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_location_entries x ON x.location_id=e.location_id AND x.entry_id=e.entry_id AND x.kind=2
 WHERE x.configuration_revision=$2
) `

func reconcileLocationTx(ctx context.Context, tx pgx.Tx, lease Lease) (int, error) {
	// Negative evidence from an older configuration or selection cannot confirm
	// absence under this retained authority. Expired run history is conservative.
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_location_edges e SET absence_suspected_run=NULL
 WHERE e.location_id=$1 AND e.absence_suspected_run IS NOT NULL AND NOT EXISTS(
 SELECT 1 FROM bloem_storage_scan_runs r WHERE r.id=e.absence_suspected_run AND r.location_id=$1
 AND r.configuration_revision=$2 AND r.selection_revision=$3 AND r.state='complete')`, lease.LocationID, lease.ConfigurationRevision, lease.SelectionRevision); err != nil {
		return 0, err
	}
	// An empty full listing is suspect and confirms no prior omission.
	if _, err := tx.Exec(ctx, `DELETE FROM bloem_storage_location_edges e USING bloem_storage_scan_directories d
 WHERE e.location_id=$1 AND d.run_id=$2 AND d.directory_id=e.parent_directory_id AND d.complete
 AND (d.listing_mode='incremental' OR (d.listing_mode='full' AND d.processed_count>0))
 AND e.absence_suspected_run IS NOT NULL AND e.absence_suspected_run<>$2
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries x WHERE x.run_id=$2 AND x.directory_id=e.parent_directory_id AND x.entry_id=e.entry_id AND NOT x.removed)`, lease.LocationID, lease.RunID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_location_edges e SET absence_suspected_run=$2
 FROM bloem_storage_scan_directories d WHERE e.location_id=$1 AND d.run_id=$2 AND d.directory_id=e.parent_directory_id
 AND d.complete AND d.listing_mode='full' AND d.processed_count>0 AND e.absence_suspected_run IS NULL
 AND NOT EXISTS(SELECT 1 FROM bloem_storage_scan_directory_entries x WHERE x.run_id=$2 AND x.directory_id=e.parent_directory_id AND x.entry_id=e.entry_id AND NOT x.removed)`, lease.LocationID, lease.RunID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `UPDATE bloem_storage_file_roots SET enabled=false WHERE location_id=$1`, lease.LocationID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, completedLocationReachSQL+`INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id)
 SELECT DISTINCT $1,f.media_file_id,r.root_id FROM reach r
 JOIN bloem_storage_location_edges e ON e.location_id=$1 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
 JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
 WHERE f.configuration_revision=$2
 ON CONFLICT(location_id,media_file_id,root_id) DO UPDATE SET enabled=true`, lease.LocationID, lease.ConfigurationRevision); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, completedLocationReachSQL+`UPDATE bloem_storage_location_entries e SET absence_confirmed_at=CASE WHEN EXISTS(
 SELECT 1 FROM bloem_storage_location_edges edge JOIN reach r ON r.directory_id=edge.parent_directory_id
 WHERE edge.location_id=e.location_id AND edge.entry_id=e.entry_id) THEN NULL ELSE clock_timestamp() END WHERE e.location_id=$1`, lease.LocationID, lease.ConfigurationRevision); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `UPDATE media_files f SET missing_since=COALESCE(f.missing_since,clock_timestamp()) FROM bloem_storage_file_refs x
 WHERE x.location_id=$1 AND x.media_file_id=f.id AND f.missing_since IS NULL AND NOT EXISTS(SELECT 1 FROM bloem_storage_file_roots m JOIN bloem_storage_selected_roots r ON r.location_id=m.location_id AND r.id=m.root_id WHERE m.location_id=x.location_id AND m.media_file_id=x.media_file_id AND m.enabled AND r.enabled)`, lease.LocationID)
	return int(tag.RowsAffected()), err
}
