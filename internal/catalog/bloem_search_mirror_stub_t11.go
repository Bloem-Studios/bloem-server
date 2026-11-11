package catalog

// Placeholder for Task 11 of the search mirror plan, so parallel tasks compile.
// The Task 11 implementer deletes this file and writes the real code.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type SearchMirrorAwarePruner struct {
	repo *SearchIndexEventRepository
	pool *pgxpool.Pool
}

func NewSearchMirrorAwarePruner(pool *pgxpool.Pool) *SearchMirrorAwarePruner {
	panic("search mirror: task 11 placeholder")
}

func (p *SearchMirrorAwarePruner) PruneProcessed(ctx context.Context, provider string, cutoff time.Time, afterID int64, limit int) (int64, int64, error) {
	panic("search mirror: task 11 placeholder")
}
