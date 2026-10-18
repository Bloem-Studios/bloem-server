package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/recommendations"
	"github.com/Silo-Server/silo-server/internal/sections"
)

type fakeSimilarEngine struct {
	scored []recommendations.ScoredItem
	err    error
}

func (f *fakeSimilarEngine) SimilarItems(context.Context, int, string, string, int, catalog.AccessFilter) ([]recommendations.ScoredItem, error) {
	return f.scored, f.err
}

func (f *fakeSimilarEngine) BecauseYouWatched(context.Context, int, string, string, int, catalog.AccessFilter) ([]recommendations.ScoredItem, error) {
	return nil, nil
}

func (f *fakeSimilarEngine) GetTasteProfileSummary(context.Context, int, string) (*recommendations.TasteProfileSummary, error) {
	return nil, nil
}

type fakeSimilarFetcher struct {
	items      map[string]*models.MediaItem
	lastAccess catalog.AccessFilter
}

func (f *fakeSimilarFetcher) FetchItemsByContentIDs(_ context.Context, contentIDs []string, filter catalog.AccessFilter) ([]*models.MediaItem, error) {
	f.lastAccess = filter
	out := make([]*models.MediaItem, 0, len(contentIDs))
	for _, id := range contentIDs {
		if item, ok := f.items[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeSimilarFetcher) FetchEpisodesByContentIDs(context.Context, []string, catalog.AccessFilter) ([]*models.MediaItem, map[string]sections.SectionItemMeta, error) {
	return nil, nil, nil
}

func (f *fakeSimilarFetcher) ListOverlaySummaries(context.Context, []string, catalog.AccessFilter) (map[string]*models.OverlaySummary, error) {
	return nil, nil
}

type fakeSimilarPresigner struct{}

func (fakeSimilarPresigner) PresignURL(_ context.Context, path string, _ string) string {
	if path == "" {
		return ""
	}
	return "https://images.example/" + path
}

func (p fakeSimilarPresigner) PresignURLsWithExpiry(ctx context.Context, paths []string, variant string) map[string]catalog.ResolvedImageURL {
	out := make(map[string]catalog.ResolvedImageURL, len(paths))
	for _, path := range paths {
		out[path] = catalog.ResolvedImageURL{URL: p.PresignURL(ctx, path, variant)}
	}
	return out
}

func newSimilarResolvedRequest(t *testing.T, itemID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/recommendations/similar/"+itemID+"/resolved", nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("item_id", itemID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	ctx = apimw.SetClaims(ctx, &auth.Claims{UserID: 11, Role: "user", TokenType: auth.TokenTypeAccess})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	ctx = access.SetScope(ctx, access.Scope{UserID: 11, ProfileID: "profile-1"})
	return req.WithContext(ctx)
}

// TestHandleSimilarResolvedPreservesTheRecommendersOrder proves the response is
// rebuilt in SimilarItems' own relevance order, not the batched fetch's
// answering order — the whole point of a rail resolving references rather
// than a client re-sorting a title list.
func TestHandleSimilarResolvedPreservesTheRecommendersOrder(t *testing.T) {
	engine := &fakeSimilarEngine{scored: []recommendations.ScoredItem{
		{MediaItemID: "b", Score: 0.9, Reason: "genre"},
		{MediaItemID: "a", Score: 0.8, Reason: "cast"},
	}}
	fetcher := &fakeSimilarFetcher{items: map[string]*models.MediaItem{
		// Answered in the opposite order the recommender named them.
		"a": {ContentID: "a", Type: "movie", Title: "Alpha", Year: 2024, PosterPath: "alpha.jpg"},
		"b": {ContentID: "b", Type: "movie", Title: "Bravo", Year: 2025},
	}}
	handler := NewRecommendationsHandler(engine, nil, nil, nil, nil, true)
	handler.Fetcher = fetcher
	handler.accessibleIDs = fakeAccessibleIDs{}
	handler.DetailSvc = fakeSimilarPresigner{}

	rr := httptest.NewRecorder()
	handler.HandleSimilarResolved(rr, newSimilarResolvedRequest(t, "origin"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var body resolvedSimilarItemsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0].ContentID != "b" || body.Items[1].ContentID != "a" {
		t.Fatalf("items = %+v, want [b a] in that order", body.Items)
	}
	if body.Items[1].PosterURL != "https://images.example/alpha.jpg" {
		t.Errorf("poster URL = %q, want the presigned alpha poster", body.Items[1].PosterURL)
	}
}

// TestHandleSimilarResolvedDropsAReferenceTheFetchCouldNotAnswer proves a
// title the batched fetch omitted — access revoked, or simply gone — is
// dropped from the response rather than rendered with blank fields.
func TestHandleSimilarResolvedDropsAReferenceTheFetchCouldNotAnswer(t *testing.T) {
	engine := &fakeSimilarEngine{scored: []recommendations.ScoredItem{
		{MediaItemID: "missing", Score: 0.9},
		{MediaItemID: "present", Score: 0.5},
	}}
	fetcher := &fakeSimilarFetcher{items: map[string]*models.MediaItem{
		"present": {ContentID: "present", Type: "movie", Title: "Present"},
	}}
	handler := NewRecommendationsHandler(engine, nil, nil, nil, nil, true)
	handler.Fetcher = fetcher
	handler.accessibleIDs = fakeAccessibleIDs{}

	rr := httptest.NewRecorder()
	handler.HandleSimilarResolved(rr, newSimilarResolvedRequest(t, "origin"))

	var body resolvedSimilarItemsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].ContentID != "present" {
		t.Fatalf("items = %+v, want only [present]", body.Items)
	}
}

func TestHandleSimilarResolvedDisabledEnginePassesTheRequestsAccessFilterThrough(t *testing.T) {
	engine := &fakeSimilarEngine{scored: []recommendations.ScoredItem{{MediaItemID: "a", Score: 1}}}
	fetcher := &fakeSimilarFetcher{items: map[string]*models.MediaItem{
		"a": {ContentID: "a", Type: "movie", Title: "Alpha"},
	}}
	handler := NewRecommendationsHandler(engine, nil, nil, nil, nil, true)
	handler.Fetcher = fetcher
	handler.accessibleIDs = fakeAccessibleIDs{}

	rr := httptest.NewRecorder()
	handler.HandleSimilarResolved(rr, newSimilarResolvedRequest(t, "origin"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if fetcher.lastAccess.ProfileID != "profile-1" {
		t.Errorf("access filter profile = %q, want profile-1", fetcher.lastAccess.ProfileID)
	}
}

// An anchor the viewer cannot see answers an empty rail, as the shared
// SimilarItems service does for /similar: the engine is not asked.
func TestHandleSimilarResolvedHidesAnInvisibleAnchor(t *testing.T) {
	engine := &fakeSimilarEngine{scored: []recommendations.ScoredItem{{MediaItemID: "a", Score: 1}}}
	fetcher := &fakeSimilarFetcher{items: map[string]*models.MediaItem{
		"a": {ContentID: "a", Type: "movie", Title: "Alpha"},
	}}
	handler := NewRecommendationsHandler(engine, nil, nil, nil, nil, true)
	handler.Fetcher = fetcher
	handler.accessibleIDs = fakeAccessibleIDs{hidden: map[string]struct{}{"origin": {}}}

	rr := httptest.NewRecorder()
	handler.HandleSimilarResolved(rr, newSimilarResolvedRequest(t, "origin"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body resolvedSimilarItemsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 0 {
		t.Fatalf("items = %+v, want none for a hidden anchor", body.Items)
	}
}
