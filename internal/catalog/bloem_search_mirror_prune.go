package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SearchMirrorAwarePruner is the daily cleanup of the search outbox when a search mirror may
// need rows that Silo's own cleanup would delete. It reads the mirror's state and nothing
// else: it never writes it. With no mirror row, no hold, or a hold older than the cap, it is
// Silo's PruneProcessed unchanged.
type SearchMirrorAwarePruner struct {
	repo *SearchIndexEventRepository
	pool *pgxpool.Pool
}

// NewSearchMirrorAwarePruner returns the pruner for pool. It is installed whether or not a
// mirror is configured here: another Silo copy sharing the database may have one.
func NewSearchMirrorAwarePruner(pool *pgxpool.Pool) *SearchMirrorAwarePruner {
	return &SearchMirrorAwarePruner{repo: NewSearchIndexEventRepository(pool), pool: pool}
}

// searchMirrorSiloPruneSQL is a verbatim copy of the query in Silo's
// (*SearchIndexEventRepository).PruneProcessed in search_index_repo.go. When Silo changes that
// query, update this copy and re-check the hold below.
const searchMirrorSiloPruneSQL = `
		WITH candidates AS (
			SELECT events.id
			FROM catalog_search_index_events AS events
			JOIN catalog_search_index_state AS state
			  ON state.provider = events.provider
			WHERE events.provider = $1
			  AND events.id <= state.last_processed_event_id
			  AND events.processed_at < $2
			  AND events.id > $4
			  AND (
			      (events.attempts < $3 AND events.last_error = '')
			      OR (
			          events.attempts >= $3
			          AND events.last_error <> ''
			          AND state.last_rebuild_at IS NOT NULL
			          AND state.last_rebuild_at >= events.processed_at
			      )
			  )
			ORDER BY events.id
			LIMIT $5
			FOR UPDATE OF events SKIP LOCKED
		), deleted AS (
			DELETE FROM catalog_search_index_events AS events
			USING candidates
			WHERE events.id = candidates.id
			RETURNING events.id
		)
		SELECT COUNT(*), COALESCE(MAX(id), $4)
		FROM deleted
`

// searchMirrorHoldPredicate keeps an outbox row until the transaction that wrote it is visible
// in the hold snapshot, that is, until the mirror has applied it. A row with no tx_id predates
// the mirror and is not held: the first full copy covers it. $6 is the hold snapshot.
const searchMirrorHoldPredicate = `AND (events.tx_id IS NULL OR pg_visible_in_snapshot(events.tx_id, $6::pg_snapshot))`

// searchMirrorHeldPruneSQL is Silo's query with the hold predicate right after its keyset
// condition.
var searchMirrorHeldPruneSQL = strings.Replace(searchMirrorSiloPruneSQL,
	"AND events.id > $4", "AND events.id > $4\n\t\t\t\t  "+searchMirrorHoldPredicate, 1)

// PruneProcessed deletes one bounded batch of processed outbox events, as Silo's does, except
// that rows the search mirror has not applied yet are kept (spec section 8). A mirror that is
// further behind than its cap holds nothing: it makes a full copy on its next run.
func (p *SearchMirrorAwarePruner) PruneProcessed(ctx context.Context, provider string, cutoff time.Time, afterID int64, limit int) (deleted, lastID int64, err error) {
	if p == nil || p.pool == nil || limit <= 0 {
		return 0, afterID, nil
	}
	hold, held, err := p.holdSnapshot(ctx)
	if err != nil {
		return 0, afterID, err
	}
	if !held {
		return p.repo.PruneProcessed(ctx, provider, cutoff, afterID, limit)
	}
	err = p.pool.QueryRow(ctx, searchMirrorHeldPruneSQL,
		provider, cutoff, searchIndexEventMaxAttempts, afterID, limit, hold).Scan(&deleted, &lastID)
	if isSearchIndexSchemaUnavailable(err) {
		return 0, afterID, nil
	}
	if err != nil {
		return 0, afterID, err
	}
	return deleted, lastID, nil
}

// holdSnapshot is the snapshot whose later outbox rows the mirror still needs; held is false
// when it needs none: there is no mirror row, nothing is held, or the hold is past the cap
// (database clock). A cap the mirror has not recorded yet is DefaultSearchMirrorMaxLag, so rows
// are never held forever; a hold time it has not recorded keeps the hold, the side that loses
// no rows.
func (p *SearchMirrorAwarePruner) holdSnapshot(ctx context.Context) (snapshot string, held bool, err error) {
	row, ok, err := loadSearchMirrorRow(ctx, p.pool)
	if isSearchIndexSchemaUnavailable(err) || (err == nil && !ok) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	snapshot, at, ok := row.State.hold()
	if !ok {
		return "", false, nil
	}
	if at.IsZero() {
		return snapshot, true, nil
	}
	maxLagSeconds := row.State.MaxLagSeconds
	if maxLagSeconds <= 0 {
		maxLagSeconds = int64(DefaultSearchMirrorMaxLag / time.Second)
	}
	var pastCap bool
	if err := p.pool.QueryRow(ctx, `SELECT now() - $1::timestamptz > $2::bigint * interval '1 second'`,
		at, maxLagSeconds).Scan(&pastCap); err != nil {
		return "", false, fmt.Errorf("compare search mirror hold with its cap: %w", err)
	}
	if pastCap {
		return "", false, nil
	}
	return snapshot, true, nil
}
