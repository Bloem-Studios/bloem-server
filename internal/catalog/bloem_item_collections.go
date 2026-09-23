package catalog

import (
	"context"
	"fmt"
	"strings"
)

// ItemCollectionsLimit bounds how many collections ListContainingItem returns.
// A title sits in a handful of collections in practice; the cap keeps a
// pathological catalog (an item every template bundle picked up) from turning
// a detail-page row into an unbounded response.
const ItemCollectionsLimit = 100

// ItemCollectionMembership is one visible server collection that contains an
// item, resolved to the single library the viewer reaches it through.
type ItemCollectionMembership struct {
	ID                string
	Title             string
	CollectionType    string
	LibraryID         int
	LibraryName       string
	GroupID           *string
	GroupName         string
	Featured          bool
	PosterURL         string
	PosterThumbhash   string
	BackdropURL       string
	BackdropThumbhash string
	ItemCount         int
}

// ListContainingItem returns the visible server (admin library) collections
// that contain contentID, as the viewer described by filter may see them.
//
// Visibility follows GET /collections/server exactly: the collection is
// 'visible', it is scoped (library_collection_libraries) to an enabled library,
// and that library passes the viewer's allow/deny lists. The caller is
// responsible for checking that the item itself is accessible
// (ItemRepository.EnsureAccessible); this query only decides which collections
// to name.
//
// Smart collections are excluded: their membership is evaluated from
// query_definition at read time (IsLiveQueryType) and is never materialized in
// library_collection_items, so answering for them would mean running every
// smart query per detail-page view. Manual, imported (mdblist/tmdb/trakt) and
// template-bundle collections are all materialized and all included.
//
// A collection scoped to several accessible libraries is returned once, via the
// library the item itself belongs to when there is one, else the first library
// in the admin's library order. ItemCount mirrors /collections/server: the raw
// membership count, not narrowed to the viewer's access.
//
// Query plan: idx_library_collection_items_media_item_id finds the item's
// membership rows, the collection, scope, library and group rows are key
// lookups, and the count is an index scan on collection_id per returned
// collection. One round trip; no per-collection queries.
func (r *LibraryCollectionRepository) ListContainingItem(ctx context.Context, contentID string, filter AccessFilter) ([]ItemCollectionMembership, error) {
	contentID = strings.TrimSpace(contentID)
	if contentID == "" {
		return []ItemCollectionMembership{}, nil
	}
	// An empty (non-nil) allowlist means "no libraries allowed".
	if filter.AllowedLibraryIDs != nil && len(filter.AllowedLibraryIDs) == 0 {
		return []ItemCollectionMembership{}, nil
	}
	query, args := buildListContainingItemSQL(contentID, filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing collections containing item: %w", err)
	}
	defer rows.Close()

	out := []ItemCollectionMembership{}
	for rows.Next() {
		var m ItemCollectionMembership
		if err := rows.Scan(&m.ID, &m.Title, &m.CollectionType, &m.LibraryID, &m.LibraryName, &m.GroupID, &m.GroupName,
			&m.Featured, &m.PosterURL, &m.PosterThumbhash, &m.BackdropURL, &m.BackdropThumbhash, &m.ItemCount); err != nil {
			return nil, fmt.Errorf("scanning collection containing item: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating collections containing item: %w", err)
	}
	return out, nil
}

func buildListContainingItemSQL(contentID string, filter AccessFilter) (string, []any) {
	args := []any{contentID}
	conditions := []string{
		"lci.media_item_id = $1",
		"lc.visibility = '" + LibraryCollectionVisibilityVisible + "'",
		"lc.collection_type <> 'smart'",
		"mf.enabled",
	}
	if filter.AllowedLibraryIDs != nil {
		args = append(args, filter.AllowedLibraryIDs)
		conditions = append(conditions, fmt.Sprintf("lcl.library_id = ANY($%d)", len(args)))
	}
	if len(filter.DisabledLibraryIDs) > 0 {
		args = append(args, filter.DisabledLibraryIDs)
		conditions = append(conditions, fmt.Sprintf("NOT (lcl.library_id = ANY($%d))", len(args)))
	}
	args = append(args, ItemCollectionsLimit)
	limitIdx := len(args)

	query := fmt.Sprintf(`
		SELECT m.id, m.title, m.collection_type, m.library_id, m.library_name, m.group_id, m.group_name,
		       m.featured, m.poster_url, m.poster_thumbhash, m.backdrop_url, m.backdrop_thumbhash,
		       (SELECT COUNT(*) FROM library_collection_items c WHERE c.collection_id = m.id)::int
		FROM (
			SELECT DISTINCT ON (lc.id)
			       lc.id, lc.title, lc.collection_type, lcl.library_id, mf.name AS library_name,
			       lcl.group_id, COALESCE(g.name, '') AS group_name,
			       lc.featured, lc.poster_url, lc.poster_thumbhash, lc.backdrop_url, lc.backdrop_thumbhash,
			       mf.sort_order AS library_sort, COALESCE(lcl.sort_order, lc.sort_order) AS collection_sort
			FROM library_collection_items lci
			JOIN library_collections lc ON lc.id = lci.collection_id
			JOIN library_collection_libraries lcl ON lcl.collection_id = lc.id
			JOIN media_folders mf ON mf.id = lcl.library_id
			LEFT JOIN library_collection_groups g ON g.id = lcl.group_id
			WHERE %s
			ORDER BY lc.id,
			         EXISTS (SELECT 1 FROM media_item_libraries mil
			                 WHERE mil.content_id = $1 AND mil.media_folder_id = lcl.library_id) DESC,
			         mf.sort_order ASC, mf.id ASC
		) m
		ORDER BY m.library_sort ASC, m.library_id ASC, m.featured DESC, m.collection_sort ASC, m.title ASC, m.id ASC
		LIMIT $%d
	`, strings.Join(conditions, " AND "), limitIdx)
	return query, args
}
