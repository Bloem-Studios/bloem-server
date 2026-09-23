package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	"github.com/Silo-Server/silo-server/internal/catalog"
)

type fakeItemAccess struct {
	accessible map[string]bool
	err        error
	gotFilter  catalog.AccessFilter
}

func (f *fakeItemAccess) EnsureAccessible(_ context.Context, id string, filter catalog.AccessFilter) error {
	f.gotFilter = filter
	if f.err != nil {
		return f.err
	}
	if !f.accessible[id] {
		return catalog.ErrItemNotFound
	}
	return nil
}

// fakeItemCollections applies the viewer's library allowlist the way the real
// query does, so the handler test proves the filter reaches the store intact.
type fakeItemCollections struct {
	rows   []catalog.ItemCollectionMembership
	err    error
	called bool
}

func (f *fakeItemCollections) ListContainingItem(_ context.Context, _ string, filter catalog.AccessFilter) ([]catalog.ItemCollectionMembership, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	out := []catalog.ItemCollectionMembership{}
	for _, r := range f.rows {
		if filter.AllowedLibraryIDs != nil && !slices.Contains(filter.AllowedLibraryIDs, r.LibraryID) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func itemCollectionsRequest(contentID string, scope *access.Scope) *http.Request {
	req := httptest.NewRequest(http.MethodGet, NativeAPIPrefix+"/catalog/items/"+url.PathEscape(contentID)+"/collections", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("content_id", contentID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	if scope != nil {
		ctx = access.SetScope(ctx, *scope)
	}
	return req.WithContext(ctx)
}

func TestHandleListItemCollections_MapsVisibleCollections(t *testing.T) {
	group := "grp-1"
	store := &fakeItemCollections{rows: []catalog.ItemCollectionMembership{
		{ID: "c1", Title: "Heist Films", CollectionType: "manual", LibraryID: 1, LibraryName: "Movies", GroupID: &group, GroupName: "Genres",
			Featured: true, PosterURL: "https://img.example/p.jpg", PosterThumbhash: "th", ItemCount: 7},
		{ID: "c2", Title: "Hidden Library Pick", CollectionType: "tmdb", LibraryID: 2, LibraryName: "Kids", ItemCount: 3},
	}}
	items := &fakeItemAccess{accessible: map[string]bool{"movie:heat-1995": true}}
	h := NewItemCollectionsHandler(store, items, nil)

	rec := httptest.NewRecorder()
	h.HandleListItemCollections(rec, itemCollectionsRequest("movie:heat-1995", &access.Scope{AllowedLibraryIDs: []int{1}, MaxContentRating: "R"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !slices.Equal(items.gotFilter.AllowedLibraryIDs, []int{1}) || items.gotFilter.MaxContentRating != "R" {
		t.Fatalf("access check saw filter %+v", items.gotFilter)
	}
	var body itemCollectionsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// The collection in library 2 is outside the profile's allowlist.
	if len(body.Collections) != 1 {
		t.Fatalf("collections = %+v", body.Collections)
	}
	got := body.Collections[0]
	if got.ID != "c1" || got.LibraryID != "1" || got.LibraryName != "Movies" || got.GroupID == nil || *got.GroupID != group ||
		got.GroupName != "Genres" || !got.Featured || got.PosterURL != "https://img.example/p.jpg" || got.ItemCount != 7 || got.CollectionType != "manual" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestHandleListItemCollections_EmptyListIsNotNull(t *testing.T) {
	h := NewItemCollectionsHandler(&fakeItemCollections{}, &fakeItemAccess{accessible: map[string]bool{"movie:x": true}}, nil)
	rec := httptest.NewRecorder()
	h.HandleListItemCollections(rec, itemCollectionsRequest("movie:x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Body.String(); got != "{\"collections\":[]}\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestHandleListItemCollections_InaccessibleItemIs404(t *testing.T) {
	store := &fakeItemCollections{rows: []catalog.ItemCollectionMembership{{ID: "c1", LibraryID: 1}}}
	h := NewItemCollectionsHandler(store, &fakeItemAccess{}, nil)
	rec := httptest.NewRecorder()
	h.HandleListItemCollections(rec, itemCollectionsRequest("movie:restricted", &access.Scope{AllowedLibraryIDs: []int{1}}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if store.called {
		t.Fatal("collections were listed for an item the viewer cannot see")
	}
}

func TestHandleListItemCollections_Errors(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name  string
		h     *ItemCollectionsHandler
		id    string
		wantS int
	}{
		{"access check fails", NewItemCollectionsHandler(&fakeItemCollections{}, &fakeItemAccess{err: boom}, nil), "movie:x", http.StatusInternalServerError},
		{"store fails", NewItemCollectionsHandler(&fakeItemCollections{err: boom}, &fakeItemAccess{accessible: map[string]bool{"movie:x": true}}, nil), "movie:x", http.StatusInternalServerError},
		{"no store", NewItemCollectionsHandler(nil, &fakeItemAccess{}, nil), "movie:x", http.StatusServiceUnavailable},
		{"blank id", NewItemCollectionsHandler(&fakeItemCollections{}, &fakeItemAccess{}, nil), " ", http.StatusBadRequest},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.h.HandleListItemCollections(rec, itemCollectionsRequest(c.id, nil))
			if rec.Code != c.wantS {
				t.Fatalf("status %d, want %d", rec.Code, c.wantS)
			}
		})
	}
}
