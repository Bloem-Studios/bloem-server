package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/recommendations"
)

// The engine remains Silo's. This adapter applies explicit bridge choices only
// to personal model rows after the reader has applied ordinary viewer policy.
type managedRecommendationReader struct {
	*recommendations.Reader
	pool *pgxpool.Pool
}

func wrapManagedRecommendationReader(reader *recommendations.Reader, pool *pgxpool.Pool) *managedRecommendationReader {
	if reader == nil {
		return nil
	}
	return &managedRecommendationReader{Reader: reader, pool: pool}
}
func (r *managedRecommendationReader) filter(ctx context.Context, user int, profile string, rows []recommendations.ForYouRow) ([]recommendations.ForYouRow, error) {
	if r.pool == nil {
		return rows, nil
	}
	records, err := r.pool.Query(ctx, `SELECT DISTINCT f.content_id FROM bloem_managed_recommendation_feedback f JOIN bloem_managed_grants g ON g.installation_id=f.installation_id AND g.active JOIN plugin_installations i ON i.id=g.installation_id AND i.enabled WHERE f.user_id=$1 AND f.profile_id=$2 AND f.dismissed`, user, profile)
	if err != nil {
		return nil, err
	}
	defer records.Close()
	hidden := map[string]bool{}
	for records.Next() {
		var id string
		if err = records.Scan(&id); err != nil {
			return nil, err
		}
		hidden[id] = true
	}
	if err = records.Err(); err != nil {
		return nil, err
	}
	out := make([]recommendations.ForYouRow, 0, len(rows))
	for _, row := range rows {
		if row.Type == "cluster" {
			items := make([]recommendations.ScoredItem, 0, len(row.Items))
			for _, item := range row.Items {
				if !hidden[item.MediaItemID] {
					items = append(items, item)
				}
			}
			row.Items = items
		}
		out = append(out, row)
	}
	return out, nil
}
func (r *managedRecommendationReader) GetForYouMain(ctx context.Context, user int, profile string, limit int, filter catalog.AccessFilter) (*recommendations.ForYouRow, error) {
	row, err := r.Reader.GetForYouMain(ctx, user, profile, limit, filter)
	if err != nil || row == nil {
		return row, err
	}
	rows, err := r.filter(ctx, user, profile, []recommendations.ForYouRow{*row})
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}
func (r *managedRecommendationReader) GetForYouRows(ctx context.Context, user int, profile string, limit int, filter catalog.AccessFilter) ([]recommendations.ForYouRow, error) {
	rows, err := r.Reader.GetForYouRows(ctx, user, profile, limit, filter)
	if err != nil {
		return nil, err
	}
	return r.filter(ctx, user, profile, rows)
}
func (r *managedRecommendationReader) GetDiscoverRows(ctx context.Context, user int, profile string, limit int, filter catalog.AccessFilter) ([]recommendations.ForYouRow, error) {
	rows, err := r.Reader.GetDiscoverRows(ctx, user, profile, limit, filter)
	if err != nil {
		return nil, err
	}
	return r.filter(ctx, user, profile, rows)
}
func (r *managedRecommendationReader) GetSection(ctx context.Context, user int, profile, kind, key string, limit int, filter catalog.AccessFilter) (*recommendations.ForYouRow, error) {
	row, err := r.Reader.GetSection(ctx, user, profile, kind, key, limit, filter)
	if err != nil || row == nil {
		return row, err
	}
	rows, err := r.filter(ctx, user, profile, []recommendations.ForYouRow{*row})
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}
