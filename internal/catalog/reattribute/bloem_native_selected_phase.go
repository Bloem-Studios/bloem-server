package reattribute

import (
	"context"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/jackc/pgx/v5"
)

func requireNativeReattributionPhase(ctx context.Context, tx pgx.Tx, opts Options, keys []string) error {
	if !catalog.NativePhaseRequest(ctx) {
		return nil
	}
	files := append([]int(nil), opts.MovedFileIDs...)
	if opts.WholeItem {
		rows, err := tx.Query(ctx, `SELECT id FROM media_files WHERE content_id=ANY($1) OR episode_id=ANY($1) OR extra_id=ANY($1)`, keys)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			files = append(files, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	selected := catalog.NativePhaseTargets{ContentIDs: []string{opts.FromContentID, opts.ToContentID}, FileIDs: files}
	for _, pair := range opts.EpisodePairs {
		selected.ContentIDs = append(selected.ContentIDs, pair.From)
		selected.Prospective = append(selected.Prospective, catalog.NativePhaseProspective{
			ContentID: pair.To, SourceIDs: []string{pair.From}, ParentIDs: []string{opts.ToContentID},
		})
	}
	return catalog.RequireNativePhase(ctx, tx, selected)
}
