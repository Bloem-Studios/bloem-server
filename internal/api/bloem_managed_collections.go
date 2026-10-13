package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/managedtracking"
	library "github.com/Silo-Server/silo-server/internal/managedtracking/librarywire"
	"github.com/Silo-Server/silo-server/internal/usercollections"
	"github.com/Silo-Server/silo-server/internal/userstore"
)

func managedLibraryFilter(scope access.Scope) catalog.AccessFilter {
	return catalog.AccessFilter{UserID: scope.UserID, ProfileID: scope.ProfileID, AllowedLibraryIDs: scope.AllowedLibraryIDs, DisabledLibraryIDs: scope.DisabledLibraryIDs, MaturityLimits: scope.MaturityLimits, MaxPlaybackQuality: scope.MaxPlaybackQuality}
}
func (s *managedCalendarSource) readManagedCollections(ctx context.Context, p managedtracking.Profile, q managedtracking.LibraryQuery, scope access.Scope) (managedtracking.LibraryData, error) {
	out := managedtracking.LibraryData{}
	if s.stores == nil {
		return out, managedtracking.ErrAuthority
	}
	store, err := s.stores.ForUser(ctx, scope.UserID)
	if err != nil {
		return out, err
	}
	personal, err := store.ListCollections(ctx, p.ProfileID)
	if err != nil {
		return out, err
	}
	pager, ok := store.(userstore.CollectionItemsPager)
	if !ok {
		return out, managedtracking.ErrAuthority
	}
	rows := []*library.LibraryCollection{}
	for _, c := range personal {
		revision, e := pager.CollectionRevision(ctx, c.ID)
		if e != nil {
			return out, e
		}
		kind := "personal"
		if c.CreatorProfileID != p.ProfileID {
			kind = "shared"
		}
		rows = append(rows, &library.LibraryCollection{Id: "personal:" + c.ID, Name: c.Name, Description: c.Description, Scope: kind, Writable: c.CreatorProfileID == p.ProfileID && c.CollectionType == "manual", Revision: uint64(revision), DefinitionKind: c.CollectionType})
	}
	repo := catalog.NewLibraryCollectionRepository(s.pool)
	server, err := repo.ListAll(ctx, nil, catalog.ListLibraryCollectionsOptions{})
	if err != nil {
		return out, err
	}
	for _, c := range server {
		if !catalog.CanAccessLibraryCollection(c, managedLibraryFilter(scope)) {
			continue
		}
		revision, e := repo.CollectionRevision(ctx, c.ID)
		if e != nil {
			return out, e
		}
		rows = append(rows, &library.LibraryCollection{Id: "server:" + c.ID, Name: c.Title, Description: c.Description, Scope: "server", Revision: uint64(revision), DefinitionKind: c.CollectionType})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Id < rows[j].Id })
	for _, row := range rows {
		if row.Id <= q.Position || q.Query != "" && !strings.Contains(strings.ToLower(row.Name), strings.ToLower(q.Query)) {
			continue
		}
		if len(out.Collections) == q.Limit {
			out.Next = out.Collections[len(out.Collections)-1].Id
			break
		}
		out.Collections = append(out.Collections, row)
	}
	return out, nil
}
func (s *managedCalendarSource) readManagedCollectionItems(ctx context.Context, p managedtracking.Profile, q managedtracking.LibraryQuery, scope access.Scope) (managedtracking.LibraryData, error) {
	out := managedtracking.LibraryData{}
	if s.stores == nil {
		return out, managedtracking.ErrAuthority
	}
	var after *catalog.QueryCursor
	if q.Position != "" {
		after = new(catalog.QueryCursor)
		if err := json.Unmarshal([]byte(q.Position), after); err != nil {
			return out, managedtracking.ErrSnapshot
		}
	}
	source, id, ok := strings.Cut(q.ParentID, ":")
	if !ok || id == "" {
		return out, managedtracking.ErrSnapshot
	}
	var kind catalog.CatalogSource
	switch source {
	case "personal":
		kind = catalog.CatalogSourceUserCollection
	case "server":
		kind = catalog.CatalogSourceLibraryCollection
	default:
		return out, managedtracking.ErrSnapshot
	}
	parent := &library.LibraryCollection{Id: q.ParentID}
	if source == "personal" {
		store, e := s.stores.ForUser(ctx, scope.UserID)
		if e != nil {
			return out, e
		}
		c, e := store.GetCollection(ctx, id)
		if e != nil || !catalog.ProfileCanAccessCollection(c, p.ProfileID) {
			return out, managedtracking.ErrAuthority
		}
		parent.Name = c.Name
		parent.Description = c.Description
		parent.Scope = "personal"
		if c.CreatorProfileID != p.ProfileID {
			parent.Scope = "shared"
		}
		parent.Writable = c.CreatorProfileID == p.ProfileID && c.CollectionType == "manual"
		parent.DefinitionKind = c.CollectionType
	} else {
		c, e := catalog.NewLibraryCollectionRepository(s.pool).GetByID(ctx, id)
		if e != nil || !catalog.CanAccessLibraryCollection(c, managedLibraryFilter(scope)) {
			return out, managedtracking.ErrAuthority
		}
		parent.Name = c.Title
		parent.Description = c.Description
		parent.Scope = "server"
		parent.DefinitionKind = c.CollectionType
	}
	resolver := catalog.NewCatalogResolver(catalog.NewBrowseRepository(s.pool), catalog.NewItemRepository(s.pool)).WithUserStoreProvider(s.stores).WithPersonalCollectionAccess(usercollections.NewOwnerAccess(s.resolver))
	page, err := resolver.Resolve(ctx, catalog.CatalogRequest{Source: kind, CollectionID: id, CursorPaging: true, After: after, Limit: q.Limit, SkipTotal: true, SnapshotAt: &q.SnapshotAt, Scope: "bloem.library.collection"}, managedLibraryFilter(scope))
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
	if err = s.addManagedLibraryIdentities(ctx, out.Items); err != nil {
		return out, err
	}
	if page.CursorScope != nil && page.CursorScope.Collection != nil {
		out.Revision = uint64(page.CursorScope.Collection.Revision)
		parent.Revision = out.Revision
		out.Collections = []*library.LibraryCollection{parent}
	}
	if page.HasMore {
		if page.Next == nil {
			return out, managedtracking.ErrSnapshot
		}
		raw, e := json.Marshal(page.Next)
		if e != nil {
			return out, e
		}
		out.Next = string(raw)
	}
	return out, nil
}
