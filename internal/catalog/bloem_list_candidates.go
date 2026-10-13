package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/listcollections"
	"github.com/Silo-Server/silo-server/internal/models"
)

// BloemListCandidatesTx restricts indexed identity matches with the ordinary
// browse planner in the publisher's transaction. Metadata is catalog membership,
// not a claim that a file is playable or that the viewer owns a copy.
func BloemListCandidatesTx(ctx context.Context, tx pgx.Tx, filter BrowseFilters, entries []listcollections.Entry) ([]listcollections.Candidate, error) {
	out := []listcollections.Candidate{}
	if tx == nil || len(entries) > listcollections.MaxSnapshotItems {
		return nil, listcollections.ErrSnapshot
	}
	var kinds, providers, values []string
	for _, e := range entries {
		if e.Kind == "unsupported" {
			continue
		}
		for provider, value := range e.ExternalIDs {
			kinds = append(kinds, e.Kind)
			providers = append(providers, provider)
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `WITH wanted AS (SELECT * FROM unnest($1::text[],$2::text[],$3::text[]) AS w(kind,provider,value))
 SELECT content_id FROM (
 SELECT mi.content_id FROM wanted w JOIN media_items mi ON mi.type=w.kind AND w.provider='tmdb' AND mi.tmdb_id=w.value
 UNION SELECT mi.content_id FROM wanted w JOIN media_items mi ON mi.type=w.kind AND w.provider='tvdb' AND mi.tvdb_id=w.value
 UNION SELECT mi.content_id FROM wanted w JOIN media_items mi ON mi.type=w.kind AND w.provider='imdb' AND mi.imdb_id=w.value
 UNION SELECT p.content_id FROM wanted w JOIN media_item_provider_ids p ON p.item_type=w.kind AND p.provider=w.provider AND p.provider_id=w.value
 ) identities ORDER BY content_id LIMIT 10001`, kinds, providers, values)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(ids) > listcollections.MaxSnapshotItems {
		return nil, listcollections.ErrSnapshot
	}
	if len(ids) == 0 {
		return out, nil
	}
	// Hold catalog parent identities through membership and its receipt.
	rows, err = tx.Query(ctx, `SELECT content_id FROM media_items WHERE content_id=ANY($1::text[]) ORDER BY content_id FOR SHARE`, ids)
	if err != nil {
		return nil, err
	}
	_, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	filter.ContentIDs = ids
	filter.Type = "movie,series,ebook,audiobook"
	filter.Limit = listcollections.MaxSnapshotItems
	filter.MaxLimit = listcollections.MaxSnapshotItems
	filter.Offset = 0
	items, err := bloemListBrowseTx(ctx, tx, filter)
	if err != nil {
		return nil, err
	}
	byID := map[string]int{}
	for _, item := range items {
		byID[item.ContentID] = len(out)
		identities := map[string]string{}
		for provider, value := range map[string]string{"tmdb": item.TmdbID, "tvdb": item.TvdbID, "imdb": item.ImdbID} {
			if value != "" {
				identities[provider] = value
			}
		}
		out = append(out, listcollections.Candidate{ContentID: item.ContentID, Kind: item.Type, ExternalIDs: identities})
	}
	rows, err = tx.Query(ctx, `SELECT content_id,provider,provider_id FROM media_item_provider_ids WHERE content_id=ANY($1::text[]) AND provider IN('isbn','asin','tmdb','tvdb','imdb') ORDER BY content_id,provider FOR SHARE`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, provider, value string
		if err = rows.Scan(&id, &provider, &value); err != nil {
			return nil, err
		}
		if i, ok := byID[id]; ok {
			if old := out[i].ExternalIDs[provider]; old != "" && old != value {
				return nil, errors.New("conflicting catalog provider identity")
			}
			out[i].ExternalIDs[provider] = value
		}
	}
	return out, rows.Err()
}

func bloemListBrowseTx(ctx context.Context, tx pgx.Tx, filter BrowseFilters) ([]*models.MediaItem, error) {
	plan, empty, err := (&BrowseRepository{}).buildBrowsePlan(filter)
	if err != nil {
		return nil, err
	}
	if empty {
		return []*models.MediaItem{}, nil
	}
	query, args := plan.pagedSQL(false)
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items, err := scanBrowseItems(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	return items, nil
}

// BloemAuthorizeListItemsTx rechecks native catalog policy immediately before
// publication, including when policy changed since identities were matched.
func BloemAuthorizeListItemsTx(ctx context.Context, tx pgx.Tx, filter BrowseFilters, ids []string) error {
	if tx == nil || len(ids) > listcollections.MaxSnapshotItems {
		return listcollections.ErrAuthority
	}
	if len(ids) == 0 {
		return nil
	}
	filter.ContentIDs = ids
	filter.Type = "movie,series,ebook,audiobook"
	filter.Limit = listcollections.MaxSnapshotItems
	filter.MaxLimit = listcollections.MaxSnapshotItems
	filter.Offset = 0
	items, err := bloemListBrowseTx(ctx, tx, filter)
	if err != nil {
		return err
	}
	unique := map[string]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	if len(items) != len(unique) {
		return listcollections.ErrAuthority
	}
	return nil
}
