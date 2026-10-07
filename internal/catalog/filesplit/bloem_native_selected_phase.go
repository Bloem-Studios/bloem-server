package filesplit

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
)

// This is the relink UPDATE's actual target selection, independent of the
// filtered user-state EpisodePairs. Files with no old episode and partially
// moved episodes still select a destination here. Repeat after reattribution's
// key wait, before relinking; keep the actual selected rows in this transaction.
func requireNativeSplitSelection(ctx context.Context, tx pgx.Tx, opts Options, files []int, includeSource bool) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	selected := catalog.NativePhaseTargets{ContentIDs: []string{opts.ToContentID}}
	if includeSource {
		selected.ContentIDs = append(selected.ContentIDs, opts.FromContentID)
		selected.FileIDs = files
	}
	if opts.ItemType != itemTypeSeries {
		return catalog.RequireNativePhase(ctx, tx, selected)
	}
	rows, err := tx.Query(ctx, `SELECT e.content_id FROM episodes e
 JOIN media_files mf ON e.season_number=mf.season_number AND e.episode_number=mf.episode_number
 WHERE mf.id=ANY($1::bigint[]) AND e.series_id=$2
 ORDER BY e.content_id,mf.id FOR UPDATE OF e,mf`, files, opts.ToContentID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		selected.ContentIDs = append(selected.ContentIDs, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	return catalog.RequireNativePhase(ctx, tx, selected)
}
