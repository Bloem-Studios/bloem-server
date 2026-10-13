package api

import (
	"context"
	"strconv"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/models"
)

// Inventory authority deliberately excludes favorites/follows: an unrelated
// personal edit does not invalidate a catalog cursor. It retains ordinary policy.
func (s *managedCalendarSource) LibraryAuthority(ctx context.Context, p managedtracking.Profile) (string, error) {
	if s == nil || s.pool == nil {
		return "", managedtracking.ErrAuthority
	}
	inventory := *s
	inventory.repo = nil
	return inventory.Authority(ctx, p)
}
func (s *managedCalendarSource) ReadLibrary(ctx context.Context, p managedtracking.Profile, q managedtracking.LibraryQuery) (managedtracking.LibraryData, error) {
	out := managedtracking.LibraryData{}
	scope, err := s.scope(ctx, p)
	if err != nil {
		return out, err
	}
	if s.pool == nil {
		return out, managedtracking.ErrAuthority
	}
	if q.View == "book_states" {
		return s.readManagedBooks(ctx, q, scope)
	}
	if q.View == "copies" {
		return s.readManagedCopies(ctx, q, scope)
	}
	if q.View == "collections" {
		return s.readManagedCollections(ctx, p, q, scope)
	}
	if q.View == "collection_items" {
		return s.readManagedCollectionItems(ctx, p, q, scope)
	}
	if q.View != "catalog" {
		return out, managedtracking.ErrSnapshot
	}
	offset := 0
	if q.Position != "" {
		offset, err = strconv.Atoi(q.Position)
		if err != nil || offset < 1 || offset > 1000000 {
			return out, managedtracking.ErrSnapshot
		}
	}
	kind := q.Kind
	if kind == "" {
		kind = "movie,series,ebook,audiobook"
	}
	page, err := catalog.NewBrowseRepository(s.pool).BrowsePage(ctx, catalog.BrowseFilters{Type: kind, UserID: scope.UserID, ProfileID: scope.ProfileID, LibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality, SearchTerm: q.Query, Sort: catalog.BrowseSortTitle, Order: "asc", Limit: q.Limit, MaxLimit: 100, Offset: offset, SnapshotAt: &q.SnapshotAt}, false)
	if err != nil {
		return out, err
	}
	for _, item := range page.Items {
		row := libraryItem(item)
		row.Available, err = s.contentAvailable(ctx, scope, item.ContentID)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, row)
	}
	if page.HasMore {
		out.Next = strconv.Itoa(offset + len(page.Items))
	}
	if err = s.addManagedLibraryIdentities(ctx, out.Items); err != nil {
		return out, err
	}
	return out, nil
}
func libraryItem(item *models.MediaItem) *library.LibraryItem {
	ids := map[string]string{}
	for key, value := range map[string]string{"tmdb": item.TmdbID, "imdb": item.ImdbID, "tvdb": item.TvdbID} {
		if value != "" {
			ids[key] = value
		}
	}
	return &library.LibraryItem{ContentId: item.ContentID, Kind: item.Type, Title: item.Title, Year: int32(item.Year), ExternalIds: ids}
}

func (s *managedCalendarSource) addManagedLibraryIdentities(ctx context.Context, items []*library.LibraryItem) error {
	// Provider aliases include real book identities; omit provider-specific URLs.
	ids := make([]string, 0, len(items))
	byID := map[string]*library.LibraryItem{}
	for _, item := range items {
		ids = append(ids, item.ContentId)
		byID[item.ContentId] = item
	}
	if len(ids) > 0 {
		ambiguous := map[string]bool{}
		rows, e := s.pool.Query(ctx, `SELECT content_id,provider,provider_id FROM media_item_provider_ids WHERE content_id=ANY($1) AND provider IN('isbn','asin','tmdb','tvdb','imdb') ORDER BY content_id,provider,provider_id`, ids)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, provider, value string
			if e = rows.Scan(&id, &provider, &value); e != nil {
				return e
			}
			if item := byID[id]; item != nil {
				if ambiguous[id+":"+provider] {
					continue
				}
				if old := item.ExternalIds[provider]; old != "" && old != value {
					ambiguous[id+":"+provider] = true
					delete(item.ExternalIds, provider)
				} else {
					item.ExternalIds[provider] = value
				}
			}
		}
		if e = rows.Err(); e != nil {
			return e
		}
	}
	return nil
}
