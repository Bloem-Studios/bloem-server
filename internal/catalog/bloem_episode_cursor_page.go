package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/models"
)

// Bloem: episode lists sorted by air date page through episode_catalog_entries.
//
// PreviewPage answers an episode list from episode_catalog_entries, whose
// indexes serve it in milliseconds, but PreviewCursorPage, which the catalog
// page uses, always built the general episode relation: every matching
// episode, with its series' language and its air date computed per row, then
// sorted. A home row's "Explore all" over English TV episodes (815,240 of
// them, 2026-11-11) took 9 to 10 seconds a page and often hit the search
// timeout, so the page never filled.
//
// For an episode list sorted by air date this answers the cursor page from
// episode_catalog_entries instead. Its order is the general query's
// (episodeAirDateOrderBy and episodeCatalogEntryOrderBy agree), and its
// cursor keys are the same six values of the same kinds, so either path can
// continue a cursor the other wrote. The total is exact up to
// bloemEpisodeCursorCountCap rows and reported as inexact beyond that:
// counting every row cost 2 to 3 seconds even from the projection.

// bloemEpisodeCursorCountCap is a variable only so a test can lower it.
var bloemEpisodeCursorCountCap = 10000

// bloemEpisodeAirDateCursorTerms are episodeAirDateOrderBy's cursor terms over
// episode_catalog_entries and its sort joins.
func bloemEpisodeAirDateCursorTerms(descending bool) []queryCursorTerm {
	return []queryCursorTerm{
		{expression: "ece.episode_air_date", kind: cursorKindDate, descending: descending, nullsLast: true},
		{expression: queryColumnSQL("sort_series", querySortDefs["title"].columnSQL), kind: cursorKindText},
		{expression: "ece.series_id", kind: cursorKindText},
		{expression: "sort_episode.season_number", kind: cursorKindNumber, descending: descending},
		{expression: "sort_episode.episode_number", kind: cursorKindNumber, descending: descending},
		{expression: "ece.episode_id", kind: cursorKindText},
	}
}

// bloemEpisodeCursorPage answers PreviewCursorPage from episode_catalog_entries
// when def is an episode list sorted by air date. ok is false when the general
// query must answer.
func (e *QueryExecutor) bloemEpisodeCursorPage(
	ctx context.Context,
	def QueryDefinition,
	access AccessFilter,
	limit int,
	after *QueryCursor,
	includeTotal bool,
) (QueryCursorPage, bool, error) {
	q, terms, ok, err := e.bloemEpisodeCursorQuery(def, access, after)
	if err != nil || !ok {
		return QueryCursorPage{}, ok, err
	}
	if q.empty {
		return QueryCursorPage{Items: []*models.MediaItem{}, TotalExact: true}, true, nil
	}
	pageSQL, args, err := bloemEpisodeCursorPageSQL(q, terms, after, limit)
	if err != nil {
		return QueryCursorPage{}, true, err
	}
	page, pageRows, keys, ok, err := e.readBloemEpisodeCursorPage(ctx, q, def, pageSQL, args, terms, limit, includeTotal)
	if err != nil || !ok {
		return QueryCursorPage{}, ok, err
	}
	items, err := e.hydrateEpisodeCatalogEntryPage(ctx, pageRows)
	if err != nil {
		if episodeCatalogEntriesUnavailable(err) {
			return QueryCursorPage{}, false, nil
		}
		return QueryCursorPage{}, true, err
	}
	consumed := 0
	if after != nil {
		consumed = after.Consumed
	}
	if def.Limit != nil && consumed+len(items) >= *def.Limit {
		page.HasMore = false
	}
	page.Items = items
	if page.HasMore && len(items) > 0 {
		next := keys[len(keys)-1]
		next.Consumed = consumed + len(items)
		page.Next = &next
	}
	for _, item := range page.Items {
		if item.AddedAt == nil && !item.CreatedAt.IsZero() {
			item.AddedAt = new(item.CreatedAt)
		}
	}
	return page, true, nil
}

// bloemEpisodeCursorQuery is def's episode_catalog_entries query and cursor
// terms, or ok false when def is not an episode list sorted by air date.
func (e *QueryExecutor) bloemEpisodeCursorQuery(def QueryDefinition, access AccessFilter, after *QueryCursor) (episodeCatalogQuery, []queryCursorTerm, bool, error) {
	if !e.usesEpisodeCatalogQueries() || !isEpisodeCatalogScope(def.MediaScope) {
		return episodeCatalogQuery{}, nil, false, nil
	}
	if after != nil && (after.Search != nil || after.Collection != nil) {
		return episodeCatalogQuery{}, nil, false, nil
	}
	sortConfig := NormalizeQuerySort(def.Sort)
	if sortConfig.Field != querySortReleaseDate && sortConfig.Field != querySortLastAirDate {
		return episodeCatalogQuery{}, nil, false, nil
	}
	q, ok, err := e.episodeCatalogEntriesQuery(def, access)
	return q, bloemEpisodeAirDateCursorTerms(sortConfig.Order != "asc"), ok, err
}

// bloemEpisodeCursorPageSQL selects one page after the cursor, with each row's
// cursor key, and one row more to tell whether more follow.
func bloemEpisodeCursorPageSQL(q episodeCatalogQuery, terms []queryCursorTerm, after *QueryCursor, limit int) (string, []any, error) {
	args := append([]any{}, q.args...)
	seek, seekArgs, err := cursorSeekSQL(terms, after, len(args)+1)
	if err != nil {
		return "", nil, err
	}
	args = append(args, seekArgs...)
	where := strings.TrimSpace(q.where)
	if seek != "" {
		if where == "" {
			where = "WHERE " + seek
		} else {
			where += " AND (" + seek + ")"
		}
	}
	columns := make([]string, len(terms))
	for i, term := range terms {
		columns[i] = "(" + term.expression + ")::text"
	}
	args = append(args, limit+1)
	return fmt.Sprintf("%sSELECT ece.episode_id, ece.added_at, %s\n%s%s\n%s\n%s\nLIMIT $%d",
		bloemEpisodeWithClause(q), strings.Join(columns, ", "), q.from, q.sortJoin, where, q.orderBy, len(args)), args, nil
}

func bloemEpisodeWithClause(q episodeCatalogQuery) string {
	if q.withCTEs == "" {
		return ""
	}
	return "WITH " + q.withCTEs + "\n"
}

// readBloemEpisodeCursorPage reads the page and, with includeTotal, its count
// in one snapshot, as the general path does. ok is false when the general
// query must answer.
func (e *QueryExecutor) readBloemEpisodeCursorPage(
	ctx context.Context,
	q episodeCatalogQuery,
	def QueryDefinition,
	pageSQL string,
	args []any,
	terms []queryCursorTerm,
	limit int,
	includeTotal bool,
) (QueryCursorPage, []episodeCatalogEntryPageRow, []QueryCursor, bool, error) {
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return QueryCursorPage{}, nil, nil, true, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	pageRows, keys, err := scanBloemEpisodeCursorRows(ctx, tx, pageSQL, args, terms)
	if err != nil {
		if episodeCatalogEntriesUnavailable(err) {
			return QueryCursorPage{}, nil, nil, false, nil
		}
		return QueryCursorPage{}, nil, nil, true, err
	}
	if q.requiresFullPageGate && len(pageRows) <= limit {
		return QueryCursorPage{}, nil, nil, false, nil
	}
	page := QueryCursorPage{HasMore: len(pageRows) > limit}
	if page.HasMore {
		pageRows, keys = pageRows[:limit], keys[:limit]
	}
	if includeTotal {
		total, exact, err := bloemCountEpisodeCursorRows(ctx, tx, q, def)
		if err != nil {
			if episodeCatalogEntriesUnavailable(err) {
				return QueryCursorPage{}, nil, nil, false, nil
			}
			return QueryCursorPage{}, nil, nil, true, fmt.Errorf("counting episode catalog entries: %w", err)
		}
		page.Total, page.TotalExact = total, exact
	}
	if err := tx.Commit(ctx); err != nil {
		return QueryCursorPage{}, nil, nil, true, err
	}
	return page, pageRows, keys, true, nil
}

func scanBloemEpisodeCursorRows(ctx context.Context, tx pgx.Tx, sql string, args []any, terms []queryCursorTerm) ([]episodeCatalogEntryPageRow, []QueryCursor, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("querying episode catalog cursor page: %w", err)
	}
	defer rows.Close()
	var pageRows []episodeCatalogEntryPageRow
	var keys []QueryCursor
	for rows.Next() {
		var row episodeCatalogEntryPageRow
		key := QueryCursor{Keys: make([]QueryCursorValue, len(terms))}
		dest := []any{&row.episodeID, &row.addedAt}
		for i, term := range terms {
			key.Keys[i].Kind = term.kind
			dest = append(dest, &key.Keys[i].Value)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("scanning episode catalog cursor page: %w", err)
		}
		pageRows = append(pageRows, row)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterating episode catalog cursor page: %w", err)
	}
	return pageRows, keys, nil
}

// bloemCountEpisodeCursorRows counts q's rows up to the cap. exact is false
// when the list is longer than the cap; the web estimates such a total as it
// loads pages.
func bloemCountEpisodeCursorRows(ctx context.Context, tx pgx.Tx, q episodeCatalogQuery, def QueryDefinition) (int, bool, error) {
	limit := bloemEpisodeCursorCountCap + 1
	if def.Limit != nil && *def.Limit < bloemEpisodeCursorCountCap {
		limit = *def.Limit
	}
	args := append(append([]any{}, q.args...), limit)
	sql := fmt.Sprintf("%sSELECT count(*) FROM (SELECT 1 %s %s LIMIT $%d) counted", bloemEpisodeWithClause(q), q.from, q.where, len(args))
	var total int
	if err := tx.QueryRow(ctx, sql, args...).Scan(&total); err != nil {
		return 0, false, err
	}
	if total > bloemEpisodeCursorCountCap {
		return bloemEpisodeCursorCountCap, false, nil
	}
	return total, true, nil
}
