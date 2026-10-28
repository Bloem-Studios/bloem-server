package storagesource

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const nativeAudioPresentationPrefix = "bloem-storage:audiobook:"

// AudioPresentationKey identifies a book directory in one library location.
func AudioPresentationKey(locationID uuid.UUID, directoryID string) (string, error) {
	if locationID == uuid.Nil || !validText(directoryID, 1024, true) {
		return "", ErrReferenceConflict
	}
	h := sha256.New()
	_, _ = h.Write([]byte("bloem/audiobook-presentation/v1\x00"))
	_, _ = h.Write(locationID[:])
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(directoryID)))
	_, _ = h.Write(size[:])
	_, _ = h.Write([]byte(directoryID))
	return nativeAudioPresentationPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

func IsNativeAudioPresentationKey(key string) bool {
	if !strings.HasPrefix(key, nativeAudioPresentationPrefix) || len(key) != len(nativeAudioPresentationPrefix)+64 {
		return false
	}
	suffix := strings.TrimPrefix(key, nativeAudioPresentationPrefix)
	_, err := hex.DecodeString(suffix)
	return err == nil && strings.ToLower(suffix) == suffix
}

type StagedAudioTrack struct {
	EntryID, Revision, Status string
	Entry, Probe              []byte
}

func audioLeaseScope(ctx context.Context, tx pgx.Tx, lease Lease) error {
	if tx == nil || lease.LocationID == uuid.Nil || lease.RunID == uuid.Nil {
		return ErrStaleLease
	}
	var ok bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM bloem_storage_scan_runs WHERE id=$1 AND location_id=$2 AND configuration_revision=$3 AND selection_revision=$4)`, lease.RunID, lease.LocationID, lease.ConfigurationRevision, lease.SelectionRevision).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStaleLease
	}
	return nil
}

func StageAudioTracksTx(ctx context.Context, tx pgx.Tx, lease Lease, directoryID string, rows []StagedAudioTrack) error {
	if err := audioLeaseScope(ctx, tx, lease); err != nil {
		return err
	}
	for _, row := range rows {
		// A rejected candidate stays rejected for this run. This also keeps a
		// bounded overflow marker intact when earlier pages are replayed.
		if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_audio_staged_tracks(run_id,directory_id,entry_id,location_id,revision,status,entry,probe)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb) ON CONFLICT(run_id,directory_id,entry_id) DO UPDATE SET revision=EXCLUDED.revision,
 status=CASE WHEN bloem_storage_audio_staged_tracks.status='ok' THEN EXCLUDED.status ELSE bloem_storage_audio_staged_tracks.status END,
 entry=EXCLUDED.entry,probe=CASE WHEN bloem_storage_audio_staged_tracks.status='ok' THEN EXCLUDED.probe ELSE NULL END`, lease.RunID, directoryID, row.EntryID, lease.LocationID, row.Revision, row.Status, row.Entry, row.Probe); err != nil {
			return err
		}
		// Keep the probe for later scans of the same revision; staging's own
		// overflow marker is applied afterwards and never reaches it. An
		// unavailable track is kept only to have its book listed again.
		if row.Status == "ok" || row.Status == "invalid" || row.Status == "unavailable" {
			if _, err := tx.Exec(ctx, `INSERT INTO bloem_storage_audio_probes(location_id,entry_id,revision,status,probe) VALUES($1,$2,$3,$4,$5::jsonb)
 ON CONFLICT(location_id,entry_id) DO UPDATE SET revision=EXCLUDED.revision,status=EXCLUDED.status,probe=EXCLUDED.probe,probed_at=clock_timestamp()`, lease.LocationID, row.EntryID, row.Revision, row.Status, row.Probe); err != nil {
				return err
			}
		}
	}
	return nil
}

// CachedAudioProbe is a track's status and staged probe from an earlier scan.
type CachedAudioProbe struct {
	Status string
	Probe  []byte
}

// CachedAudioProbes returns the probes kept for the given entries whose
// revision is still exactly the one given.
func (r *Repository) CachedAudioProbes(ctx context.Context, locationID uuid.UUID, revisions map[string]string) (map[string]CachedAudioProbe, error) {
	ids := make([]string, 0, len(revisions))
	for id := range revisions {
		ids = append(ids, id)
	}
	out := make(map[string]CachedAudioProbe, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT entry_id,revision,status,probe FROM bloem_storage_audio_probes WHERE location_id=$1 AND entry_id=ANY($2::text[])`, locationID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, revision string
		var cached CachedAudioProbe
		if err := rows.Scan(&id, &revision, &cached.Status, &cached.Probe); err != nil {
			return nil, err
		}
		if revisions[id] == revision {
			out[id] = cached
		}
	}
	return out, rows.Err()
}

// PruneAudioProbesTx drops kept probes for entries the location no longer
// lists at the same revision.
func PruneAudioProbesTx(ctx context.Context, tx pgx.Tx, locationID uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM bloem_storage_audio_probes p WHERE p.location_id=$1 AND NOT EXISTS(SELECT 1 FROM bloem_storage_location_entries x WHERE x.location_id=p.location_id AND x.entry_id=p.entry_id AND x.revision=p.revision)`, locationID)
	return err
}

func StagedAudioTracksTx(ctx context.Context, tx pgx.Tx, lease Lease, directoryID string) ([]StagedAudioTrack, error) {
	if err := audioLeaseScope(ctx, tx, lease); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT entry_id,revision,status,entry,probe FROM bloem_storage_audio_staged_tracks WHERE run_id=$1 AND location_id=$2 AND directory_id=$3 ORDER BY entry_id FOR UPDATE`, lease.RunID, lease.LocationID, directoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tracks []StagedAudioTrack
	for rows.Next() {
		var track StagedAudioTrack
		if err := rows.Scan(&track.EntryID, &track.Revision, &track.Status, &track.Entry, &track.Probe); err != nil {
			return nil, err
		}
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}

func DeleteStagedAudioTracksTx(ctx context.Context, tx pgx.Tx, lease Lease, directoryID string) error {
	if err := audioLeaseScope(ctx, tx, lease); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM bloem_storage_audio_staged_tracks WHERE run_id=$1 AND location_id=$2 AND directory_id=$3`, lease.RunID, lease.LocationID, directoryID)
	return err
}

func DeleteStaleAudioStagingTx(ctx context.Context, tx pgx.Tx, lease Lease) (int, error) {
	if err := audioLeaseScope(ctx, tx, lease); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM bloem_storage_audio_staged_tracks WHERE location_id=$1 AND run_id<>$2`, lease.LocationID, lease.RunID)
	return int(tag.RowsAffected()), err
}

// RefreshAudioPublicationRootsTx attaches finalizer publications to the
// reconciled retained-edge graph. It never invents membership from a path.
func RefreshAudioPublicationRootsTx(ctx context.Context, tx pgx.Tx, lease Lease) error {
	if err := audioLeaseScope(ctx, tx, lease); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, completedLocationReachSQL+`INSERT INTO bloem_storage_file_roots(location_id,media_file_id,root_id)
 SELECT DISTINCT $1,f.media_file_id,r.root_id FROM reach r
 JOIN bloem_storage_location_edges e ON e.location_id=$1 AND e.parent_directory_id=r.directory_id
 JOIN bloem_storage_file_refs f ON f.location_id=e.location_id AND f.entry_id=e.entry_id
 JOIN media_files mf ON mf.id=f.media_file_id AND mf.presentation_group_key LIKE 'bloem-storage:audiobook:%' AND mf.missing_since IS NULL
 JOIN bloem_storage_location_entries x ON x.location_id=f.location_id AND x.entry_id=f.entry_id AND x.kind=1 AND x.revision=f.revision AND x.configuration_revision=f.configuration_revision
 WHERE f.configuration_revision=$2 ON CONFLICT(location_id,media_file_id,root_id) DO UPDATE SET enabled=true`, lease.LocationID, lease.ConfigurationRevision)
	return err
}

// PresentationReferences checks the whole present presentation in one snapshot.
func (r *Repository) PresentationReferences(ctx context.Context, folderID int, contentID, groupKey string) ([]int, error) {
	if folderID <= 0 || contentID == "" || !IsNativeAudioPresentationKey(groupKey) {
		return nil, ErrReferenceConflict
	}
	rows, err := r.pool.Query(ctx, `SELECT f.id,COALESCE(f.presentation_part_index,0),COALESCE(f.presentation_part_total,0),f.presentation_kind,f.file_path,
 x.location_id,x.entry_id,
 COALESCE(folder.enabled AND l.folder_id=f.media_folder_id AND x.configuration_revision=s.configuration_revision AND EXISTS(
 SELECT 1 FROM bloem_storage_file_roots m JOIN bloem_storage_selected_roots root ON root.location_id=m.location_id AND root.id=m.root_id AND root.enabled WHERE m.location_id=l.id AND m.media_file_id=f.id AND m.enabled),false),
 COALESCE(s.enabled AND i.enabled AND i.plugin_id=s.plugin_id AND i.owner_id=s.owner_id,false)
 FROM media_files f LEFT JOIN bloem_storage_file_refs x ON x.media_file_id=f.id
 LEFT JOIN library_storage_locations l ON l.id=x.location_id LEFT JOIN bloem_storage_sources s ON s.key=l.source_key
 LEFT JOIN media_folders folder ON folder.id=f.media_folder_id LEFT JOIN plugin_installations i ON i.id=s.installation_id
 WHERE f.media_folder_id=$1 AND f.content_id=$2 AND f.presentation_group_key=$3 AND f.presentation_group_key<>'' AND f.missing_since IS NULL ORDER BY f.presentation_part_index NULLS FIRST,f.id`, folderID, contentID, groupKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	total := 0
	unavailable := false
	for rows.Next() {
		var id, index, n int
		var kind, path string
		var location *uuid.UUID
		var entry *string
		var authorized, available bool
		if err := rows.Scan(&id, &index, &n, &kind, &path, &location, &entry, &authorized, &available); err != nil {
			return nil, err
		}
		if location == nil || entry == nil || !authorized {
			return nil, ErrReferenceConflict
		}
		expected, e := CatalogLocation(*location, *entry)
		if e != nil || path != expected {
			return nil, ErrReferenceConflict
		}
		if len(ids) == 0 {
			total = n
		}
		if n != total || (n == 0 && (index != 0 || kind != "")) || (n > 0 && (index != len(ids)+1 || kind != "multipart")) {
			return nil, ErrReferenceConflict
		}
		unavailable = unavailable || !available
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 || (total == 0 && len(ids) != 1) || (total > 0 && total != len(ids)) {
		return nil, ErrReferenceConflict
	}
	if unavailable {
		return nil, ErrSourceUnavailable
	}
	return ids, nil
}
