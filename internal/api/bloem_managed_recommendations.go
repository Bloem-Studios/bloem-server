package api

import (
	"context"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
	"github.com/Silo-Server/silo-server/internal/recommendations"
)

// This projection shares personal model results, excluding collaborative and
// global cold-start rows. It never invokes a separate recommendation model.
func managedAIItems(rows []recommendations.ForYouRow) []recommendations.ScoredItem {
	out := []recommendations.ScoredItem{}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Type != "cluster" {
			continue
		}
		for _, item := range row.Items {
			if seen[item.MediaItemID] {
				continue
			}
			seen[item.MediaItemID] = true
			if item.ReasonDetail == "" {
				item.ReasonDetail = row.Label
			}
			out = append(out, item)
			if len(out) == 100 {
				return out
			}
		}
	}
	return out
}
func (s *managedCalendarSource) Recommendations(ctx context.Context, p managedtracking.Profile) ([]*rec.Recommendation, error) {
	scope, err := s.scope(ctx, p)
	if err != nil {
		return nil, err
	}
	if s.recommendations == nil || s.pool == nil {
		return nil, managedtracking.ErrAuthority
	}
	filter := catalog.AccessFilter{UserID: scope.UserID, ProfileID: scope.ProfileID, AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality}
	first, err := s.recommendations.GetForYouMain(ctx, scope.UserID, scope.ProfileID, 20, filter)
	if err != nil {
		return nil, err
	}
	rest, err := s.recommendations.GetForYouRows(ctx, scope.UserID, scope.ProfileID, 20, filter)
	if err != nil {
		return nil, err
	}
	rows := []recommendations.ForYouRow{}
	if first != nil {
		rows = append(rows, *first)
	}
	rows = append(rows, rest...)
	scored := managedAIItems(rows)
	ids := make([]string, 0, len(scored))
	for _, item := range scored {
		ids = append(ids, item.MediaItemID)
	}
	if len(ids) == 0 {
		return []*rec.Recommendation{}, nil
	}
	data, err := s.pool.Query(ctx, `SELECT content_id,type,title,COALESCE(tmdb_id,''),COALESCE(imdb_id,''),COALESCE(tvdb_id,'') FROM media_items WHERE content_id=ANY($1) AND type IN ('movie','series')`, ids)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	index := map[string]*rec.Recommendation{}
	for data.Next() {
		item := &rec.Recommendation{}
		var tmdb, imdb, tvdb string
		if err = data.Scan(&item.ContentId, &item.Kind, &item.Title, &tmdb, &imdb, &tvdb); err != nil {
			return nil, err
		}
		item.ExternalIds = map[string]string{}
		for k, v := range map[string]string{"tmdb": tmdb, "imdb": imdb, "tvdb": tvdb} {
			if v != "" {
				item.ExternalIds[k] = v
			}
		}
		index[item.ContentId] = item
	}
	if err = data.Err(); err != nil {
		return nil, err
	}
	out := []*rec.Recommendation{}
	for _, score := range scored {
		item := index[score.MediaItemID]
		if item == nil {
			continue
		}
		item.Reason = score.Reason
		item.ReasonDetail = score.ReasonDetail
		out = append(out, item)
	}
	return out, nil
}
