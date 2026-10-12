package catalog

import "context"

// RestoreMaintenanceEpisodes preserves local ScanFile's episode repair without
// claiming present files owned by an excluded maintenance lane. Keep first-seen
// attribution and the series timestamp update aligned with the catalog writer.
// filePredicate is trusted SQL supplied by the host maintenance scope for alias mf.
func (r *EpisodeLibraryRepository) RestoreMaintenanceEpisodes(ctx context.Context, folderID int, filePredicate string) error {
	return retryOnDeadlock(ctx, func() error { return r.restoreMaintenanceEpisodesOnce(ctx, folderID, filePredicate) })
}

func (r *EpisodeLibraryRepository) restoreMaintenanceEpisodesOnce(ctx context.Context, folderID int, filePredicate string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var seriesIDs []string
	err = tx.QueryRow(ctx, `WITH inserted AS (
        INSERT INTO episode_libraries(episode_id,media_folder_id,first_seen_at,first_seen_scan_run_id)
        SELECT mf.episode_id,mf.media_folder_id,MIN(mf.created_at),
            (array_agg(mf.first_seen_scan_run_id ORDER BY mf.created_at ASC,mf.id ASC))[1]
        FROM media_files mf JOIN episodes e ON e.content_id=mf.episode_id
        WHERE mf.media_folder_id=$1 AND mf.missing_since IS NULL AND mf.episode_id IS NOT NULL`+" AND ("+filePredicate+")"+`
            AND NOT EXISTS (SELECT 1 FROM episode_libraries el WHERE el.episode_id=mf.episode_id AND el.media_folder_id=mf.media_folder_id)
        GROUP BY mf.episode_id,mf.media_folder_id
        ON CONFLICT(episode_id,media_folder_id) DO NOTHING RETURNING episode_id
    ) SELECT COALESCE(array_agg(DISTINCT e.series_id),ARRAY[]::text[])
        FROM inserted i JOIN episodes e ON e.content_id=i.episode_id`, folderID).Scan(&seriesIDs)
	if err != nil {
		return err
	}
	if err = RecomputeSeriesLatestEpisodeAdded(ctx, tx, seriesIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
