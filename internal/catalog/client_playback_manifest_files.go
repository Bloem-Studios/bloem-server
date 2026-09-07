package catalog

import (
	"context"
	"fmt"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/jackc/pgx/v5"
)

type clientPlaybackManifestQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type clientPlaybackManifestFileReader struct{ pool clientPlaybackManifestQuery }
type clientPlaybackManifestItemReader struct{ query clientPlaybackManifestQuery }

func (r clientPlaybackManifestItemReader) GetByIDsWithAccess(ctx context.Context, ids []string, filter AccessFilter) ([]*models.MediaItem, error) {
	sql, args := (&ItemRepository{}).buildGetByIDsWithAccessSQL(ids, filter)
	rows, err := r.query.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

// The bounded join observes anchor membership and all sibling rows in one SQL
// snapshot. Never use scanner.GetByContentID here: it omits missing parts.
const clientPlaybackManifestFilesSQL = `SELECT f.id, f.content_id,
 COALESCE(f.episode_id, ''), COALESCE(f.extra_id, ''), f.media_folder_id,
 COALESCE(f.duration, 0), COALESCE(f.probe_source, ''), f.missing_since,
 COALESCE(f.resolution, ''), COALESCE(f.presentation_kind, ''),
 COALESCE(f.presentation_group_key, ''), COALESCE(f.presentation_part_index, 0),
 COALESCE(f.presentation_part_total, 0), COALESCE(f.edition_key, '')
 FROM media_files anchor JOIN media_files f ON f.content_id = anchor.content_id
 WHERE anchor.id = $1 AND anchor.content_id IS NOT NULL
 ORDER BY f.id LIMIT $2`

func (r clientPlaybackManifestFileReader) loadClientPlaybackFiles(ctx context.Context, anchorFileID int) ([]*models.MediaFile, error) {
	rows, err := r.pool.Query(ctx, clientPlaybackManifestFilesSQL, anchorFileID, clientPlaybackManifestLimit+1)
	if err != nil {
		return nil, fmt.Errorf("read playback manifest files: %w", err)
	}
	defer rows.Close()
	files := make([]*models.MediaFile, 0)
	for rows.Next() {
		f := new(models.MediaFile)
		if err := rows.Scan(&f.ID, &f.ContentID, &f.EpisodeID, &f.ExtraID, &f.MediaFolderID,
			&f.Duration, &f.ProbeSource, &f.MissingSince, &f.Resolution, &f.PresentationKind,
			&f.PresentationGroupKey, &f.PresentationPartIndex, &f.PresentationPartTotal, &f.EditionKey); err != nil {
			return nil, fmt.Errorf("scan playback manifest file: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}
