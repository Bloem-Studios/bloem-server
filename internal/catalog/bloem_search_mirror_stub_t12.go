package catalog

// Placeholder for Task 12 of the search mirror plan, so parallel tasks compile.
// The Task 12 implementer deletes this file and writes the real code.

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StartSearchMirrorMetrics registers the collector on the default registry and unregisters it when ctx ends.
func StartSearchMirrorMetrics(ctx context.Context, pool *pgxpool.Pool, cfg SearchMirrorConfig) error {
	panic("search mirror: task 12 placeholder")
}

// searchMirrorPendingCount counts outbox rows not yet covered by the hold snapshot, capped at 1,000,000.
func searchMirrorPendingCount(ctx context.Context, q pgxQuerier, hold string) (int64, bool, error) {
	panic("search mirror: task 12 placeholder")
}

// searchMirrorDisplayState applies ruling 4's precedence (misconfigured > unreachable > failing >
// needs_full_copy > copying > waiting > settling > following); a non-empty problem forces misconfigured,
// and settling shows only while the underlying state is following. Read by Task 13.
func searchMirrorDisplayState(s searchMirrorState, problem SearchMirrorProblem) SearchMirrorStateName {
	panic("search mirror: task 12 placeholder")
}
