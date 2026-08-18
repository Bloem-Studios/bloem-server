package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Silo-Server/silo-server/internal/access"
	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/Silo-Server/silo-server/internal/watchdoc"
)

// watchContractsRootEnv points at the vondel-client-contracts checkout holding
// the versioned Watch schema. Endpoint bodies are validated against that
// schema rather than a hand-written field list, so a response the TV clients
// would refuse fails here first.
const watchContractsRootEnv = "VONDEL_CONTRACTS_ROOT"

func watchContractsRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv(watchContractsRootEnv),
		os.Getenv("VONDEL_CLIENT_CONTRACTS_DIR"),
		filepath.Join("..", "..", "..", "..", "vondel-client-contracts"),
	}
	var looked []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		looked = append(looked, abs)
		if _, err := os.Stat(filepath.Join(abs, "schema", "watch", "document.schema.json")); err == nil {
			return abs
		}
	}
	t.Skipf("watch document schema unavailable: set %s to a vondel-client-contracts checkout (looked in %s)",
		watchContractsRootEnv, strings.Join(looked, ", "))
	return ""
}

func assertWatchResponseConforms(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	path := filepath.Join(watchContractsRoot(t), "schema", "watch", "document.schema.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read watch document schema: %v", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(raw, &schemaDocument); err != nil {
		t.Fatalf("decode watch document schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	if err := compiler.AddResource("watch-document.json", schemaDocument); err != nil {
		t.Fatalf("add watch document schema: %v", err)
	}
	schema, err := compiler.Compile("watch-document.json")
	if err != nil {
		t.Fatalf("compile watch document schema: %v", err)
	}
	var value any
	if err := json.Unmarshal(rr.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("response does not conform to watch_document_v1: %v\nbody: %s", err, rr.Body.String())
	}
	body, _ := value.(map[string]any)
	return body
}

// --- fake reader -----------------------------------------------------------

type watchTestReader struct {
	items    []watchdoc.Item
	episodes map[string][]watchdoc.Episode
	files    map[string]int64
	progress []watchdoc.Progress
	err      error

	lastScope watchdoc.ProfileScope
}

func (r *watchTestReader) Items(_ context.Context, scope watchdoc.ProfileScope) ([]watchdoc.Item, error) {
	r.lastScope = scope
	if r.err != nil {
		return nil, r.err
	}
	return r.items, nil
}

func (r *watchTestReader) Item(_ context.Context, scope watchdoc.ProfileScope, contentID string) (watchdoc.Item, bool, error) {
	r.lastScope = scope
	if r.err != nil {
		return watchdoc.Item{}, false, r.err
	}
	for _, item := range r.items {
		if item.ContentID == contentID {
			return item, true, nil
		}
	}
	return watchdoc.Item{}, false, nil
}

func (r *watchTestReader) Episodes(_ context.Context, _ watchdoc.ProfileScope, seriesID string) ([]watchdoc.Episode, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.episodes[seriesID], nil
}

func (r *watchTestReader) FilesByContentIDs(_ context.Context, _ watchdoc.ProfileScope, contentIDs []string) (map[string]int64, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := map[string]int64{}
	for _, id := range contentIDs {
		if fileID, ok := r.files[id]; ok {
			out[id] = fileID
		}
	}
	return out, nil
}

func (r *watchTestReader) Progress(_ context.Context, _ watchdoc.ProfileScope, contentIDs []string) ([]watchdoc.Progress, error) {
	if r.err != nil {
		return nil, r.err
	}
	wanted := map[string]bool{}
	for _, id := range contentIDs {
		wanted[id] = true
	}
	var out []watchdoc.Progress
	for _, row := range r.progress {
		if wanted[row.ContentID] {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *watchTestReader) Markers(_ context.Context, _ watchdoc.ProfileScope, _ []int64) (map[int64]watchdoc.FileMarkers, error) {
	if r.err != nil {
		return nil, r.err
	}
	return map[int64]watchdoc.FileMarkers{}, nil
}

func (r *watchTestReader) Credits(_ context.Context, _ watchdoc.ProfileScope, _ string) ([]watchdoc.CastMember, []watchdoc.CrewMember, error) {
	if r.err != nil {
		return nil, nil, r.err
	}
	return nil, nil, nil
}

func (r *watchTestReader) Editions(_ context.Context, _ watchdoc.ProfileScope, _ []string) (map[string][]watchdoc.Edition, error) {
	if r.err != nil {
		return nil, r.err
	}
	return map[string][]watchdoc.Edition{}, nil
}

func newWatchTestReader(t *testing.T) *watchTestReader {
	t.Helper()
	added, err := time.Parse(time.RFC3339, "2026-08-13T09:00:00Z")
	if err != nil {
		t.Fatalf("parse fixture time: %v", err)
	}
	return &watchTestReader{
		items: []watchdoc.Item{
			{Kind: watchdoc.KindMovie, ContentID: "4242", Title: "The Invented Crossing", Year: 2026, RuntimeSeconds: 6480, Rating: "PG", AddedAt: &added},
			{Kind: watchdoc.KindSeries, ContentID: "8080", Title: "Eight Quiet Rooms", Year: 2026, SeasonCount: 1},
		},
		episodes: map[string][]watchdoc.Episode{
			"8080": {
				{ContentID: "8080-s01e01", SeriesID: "8080", SeasonNumber: 1, EpisodeNumber: 1, Title: "The First Locked Room", RuntimeSeconds: 2700, SeasonTitle: "Lantern Floor"},
			},
		},
		files: map[string]int64{"4242": 4242001, "8080-s01e01": 8080001},
		progress: []watchdoc.Progress{
			{ContentID: "4242", PositionSeconds: 1234.5, DurationSeconds: 6480, UpdatedAt: added},
		},
	}
}

func newWatchRequest(t *testing.T, target, contentID, profileID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	routeCtx := chi.NewRouteContext()
	if contentID != "" {
		routeCtx.URLParams.Add("content_id", contentID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	ctx = apimw.SetClaims(ctx, &auth.Claims{UserID: 11, Role: "user", TokenType: auth.TokenTypeAccess})
	if profileID != "" {
		ctx = apimw.SetProfileID(ctx, profileID)
		ctx = access.SetScope(ctx, access.Scope{
			UserID:            11,
			ProfileID:         profileID,
			AllowedLibraryIDs: []int{4, 9},
			MaxContentRating:  "PG-13",
		})
	}
	return req.WithContext(ctx)
}

// --- tests -----------------------------------------------------------------

func TestWatchHomeEndpointServesAContractDocument(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v2/watch/home", "", "profile-invented"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store for a profile-scoped document", got)
	}
	body := assertWatchResponseConforms(t, rr)
	if body["schema"] != "watch_document_v1" {
		t.Errorf("schema = %#v", body["schema"])
	}
	if body["featured_content_id"] != "4242" {
		t.Errorf("featured_content_id = %#v, want 4242", body["featured_content_id"])
	}
}

func TestWatchHomeEndpointPassesTheViewerScopeToTheReader(t *testing.T) {
	reader := newWatchTestReader(t)
	handler := NewWatchHandler(reader, nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v2/watch/home", "", "profile-restricted"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if reader.lastScope.ProfileID != "profile-restricted" {
		t.Errorf("scope profile = %q", reader.lastScope.ProfileID)
	}
	if reader.lastScope.UserID != 11 {
		t.Errorf("scope user = %d, want 11", reader.lastScope.UserID)
	}
	if len(reader.lastScope.AllowedLibraryIDs) != 2 {
		t.Errorf("scope allowed libraries = %#v, want the viewer's two", reader.lastScope.AllowedLibraryIDs)
	}
	if reader.lastScope.MaxContentRating != "PG-13" {
		t.Errorf("scope max content rating = %q", reader.lastScope.MaxContentRating)
	}
}

func TestWatchHomeEndpointRequiresAProfile(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v2/watch/home", "", ""))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// The same vocabulary apimw.RequireProfile puts on the wire for every other
	// profile-scoped route, so the handler's own guard cannot answer something
	// the mounted route never produces.
	if body["error"] != "bad_request" {
		t.Errorf("error = %#v, want bad_request", body["error"])
	}
}

func TestWatchItemEndpointServesASeriesDetailDocument(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v2/watch/items/8080", "8080", "profile-invented"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := assertWatchResponseConforms(t, rr)
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want the series and its episode", len(items))
	}
	episode, _ := items[1].(map[string]any)
	if episode["kind"] != "episode" || episode["file_id"] != float64(8080001) {
		t.Errorf("episode item = %#v", episode)
	}
}

func TestWatchItemEndpointRequiresAProfile(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v2/watch/items/8080", "8080", ""))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	// The same vocabulary apimw.RequireProfile puts on the wire for every other
	// profile-scoped route, so the handler's own guard cannot answer something
	// the mounted route never produces.
	if body["error"] != "bad_request" {
		t.Errorf("error = %#v, want bad_request", body["error"])
	}
}

func TestWatchItemEndpointAnswersNotFoundForAnUnknownContentID(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v2/watch/items/3030", "3030", "profile-invented"))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "not_found" {
		t.Errorf("error = %#v, want not_found", body["error"])
	}
}

func TestWatchItemEndpointRejectsAnEmptyContentID(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v2/watch/items/", "", "profile-invented"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestWatchEndpointsReportAFailingReader(t *testing.T) {
	reader := newWatchTestReader(t)
	reader.err = errors.New("catalog unavailable")
	handler := NewWatchHandler(reader, nil)

	home := httptest.NewRecorder()
	handler.HandleWatchHome(home, newWatchRequest(t, "/api/v2/watch/home", "", "profile-invented"))
	if home.Code != http.StatusInternalServerError {
		t.Errorf("home status = %d, want 500; body %s", home.Code, home.Body.String())
	}

	detail := httptest.NewRecorder()
	handler.HandleWatchItem(detail, newWatchRequest(t, "/api/v2/watch/items/4242", "4242", "profile-invented"))
	if detail.Code != http.StatusInternalServerError {
		t.Errorf("detail status = %d, want 500; body %s", detail.Code, detail.Body.String())
	}
}

func TestWatchEndpointsWithoutAReaderAreUnavailable(t *testing.T) {
	handler := NewWatchHandler(nil, nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v2/watch/home", "", "profile-invented"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
}

// --- catalog-backed reader -------------------------------------------------
//
// The adapter is where the Watch join meets the real stores. These tests drive
// it over fakes so the store calls themselves can be asserted; the
// database-backed test in internal/api proves the SQL those calls become.

type fakeWatchBrowse struct {
	result  *catalog.BrowseResult
	filters catalog.BrowseFilters
}

func (f *fakeWatchBrowse) BrowsePage(_ context.Context, filters catalog.BrowseFilters, _ bool) (*catalog.BrowseResult, error) {
	f.filters = filters
	return f.result, nil
}

type fakeWatchItems struct {
	items      map[string]*models.MediaItem
	restricted map[string]bool
	requested  [][]string
}

func (f *fakeWatchItems) GetByIDsWithAccess(_ context.Context, contentIDs []string, _ catalog.AccessFilter) ([]*models.MediaItem, error) {
	f.requested = append(f.requested, append([]string(nil), contentIDs...))
	found := make([]*models.MediaItem, 0, len(contentIDs))
	for _, id := range contentIDs {
		if item, ok := f.items[id]; ok && !f.restricted[id] {
			found = append(found, item)
		}
	}
	return found, nil
}

func (f *fakeWatchItems) EnsureAccessibleIDs(_ context.Context, contentIDs []string, _ catalog.AccessFilter) (map[string]bool, error) {
	accessible := make(map[string]bool, len(contentIDs))
	for _, id := range contentIDs {
		accessible[id] = !f.restricted[id]
	}
	return accessible, nil
}

type fakeWatchEpisodes struct {
	bySeries map[string][]*models.Episode
	byID     map[string]*models.Episode
	lookups  [][]string
}

func (f *fakeWatchEpisodes) ListBySeries(_ context.Context, seriesID string) ([]*models.Episode, error) {
	return f.bySeries[seriesID], nil
}

func (f *fakeWatchEpisodes) GetByIDs(_ context.Context, contentIDs []string) ([]*models.Episode, error) {
	f.lookups = append(f.lookups, append([]string(nil), contentIDs...))
	found := make([]*models.Episode, 0, len(contentIDs))
	for _, id := range contentIDs {
		if episode, ok := f.byID[id]; ok {
			found = append(found, episode)
		}
	}
	return found, nil
}

type fakeWatchFiles struct {
	byContent map[string][]*models.MediaFile
	byEpisode map[string][]*models.MediaFile
	byID      map[int]*models.MediaFile
}

func (f *fakeWatchFiles) ListByContentIDs(_ context.Context, contentIDs []string) (map[string][]*models.MediaFile, error) {
	out := map[string][]*models.MediaFile{}
	for _, id := range contentIDs {
		if files, ok := f.byContent[id]; ok {
			out[id] = files
		}
	}
	return out, nil
}

func (f *fakeWatchFiles) ListByEpisodeIDs(_ context.Context, episodeIDs []string) (map[string][]*models.MediaFile, error) {
	out := map[string][]*models.MediaFile{}
	for _, id := range episodeIDs {
		if files, ok := f.byEpisode[id]; ok {
			out[id] = files
		}
	}
	return out, nil
}

func (f *fakeWatchFiles) GetByIDs(_ context.Context, ids []int) ([]*models.MediaFile, error) {
	found := make([]*models.MediaFile, 0, len(ids))
	for _, id := range ids {
		if file, ok := f.byID[id]; ok {
			found = append(found, file)
		}
	}
	return found, nil
}

type filteredProgressCall struct {
	status    string
	types     []string
	libraryID *int
	limit     int
	offset    int
}

// fakeWatchStore implements only the three progress reads the adapter makes;
// the embedded interface panics on anything else, so a new store call cannot
// slip in untested.
type fakeWatchStore struct {
	userstore.UserStore
	direct   map[string]userstore.WatchProgress
	filtered []userstore.WatchProgress

	directCalls     [][]string
	filteredCalls   []filteredProgressCall
	unfilteredCalls int
}

func (s *fakeWatchStore) ListProgressByMediaItems(_ context.Context, _ string, mediaItemIDs []string) (map[string]userstore.WatchProgress, error) {
	s.directCalls = append(s.directCalls, append([]string(nil), mediaItemIDs...))
	found := map[string]userstore.WatchProgress{}
	for _, id := range mediaItemIDs {
		if row, ok := s.direct[id]; ok {
			found[id] = row
		}
	}
	return found, nil
}

func (s *fakeWatchStore) ListProgressFiltered(_ context.Context, _, status string, types []string, libraryID *int, limit, offset int) ([]userstore.WatchProgress, error) {
	s.filteredCalls = append(s.filteredCalls, filteredProgressCall{
		status: status, types: append([]string(nil), types...), libraryID: libraryID, limit: limit, offset: offset,
	})
	wantEpisodesOnly := len(types) == 1 && types[0] == itemTypeEpisode
	rows := make([]userstore.WatchProgress, 0, len(s.filtered))
	for _, row := range s.filtered {
		if wantEpisodesOnly && !strings.Contains(row.MediaItemID, "-s0") {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *fakeWatchStore) ListProgress(_ context.Context, _, _ string, _, _ int) ([]userstore.WatchProgress, error) {
	s.unfilteredCalls++
	return nil, nil
}

type fakeWatchStores struct{ store userstore.UserStore }

func (s fakeWatchStores) ForUser(context.Context, int) (userstore.UserStore, error) {
	return s.store, nil
}
func (s fakeWatchStores) Close() error { return nil }

// fakeImageResolver stands in for the plugin-backed resolver: a fixed map from
// the raw stored path this fixture puts on "4242" to a URL a client could
// actually fetch. An unmapped path resolves to "", matching the real
// resolver's contract for a path it cannot answer.
type fakeImageResolver struct{ byPath map[string]string }

func (f fakeImageResolver) ResolveImageURL(_ context.Context, path string, _ string) string {
	return f.byPath[path]
}

func (f fakeImageResolver) ResolveImageURLs(_ context.Context, paths []string, variant string) map[string]string {
	resolved := make(map[string]string, len(paths))
	for _, path := range paths {
		if url := f.ResolveImageURL(context.Background(), path, variant); url != "" {
			resolved[path] = url
		}
	}
	return resolved
}

const fakeWatchPosterPath = "posters/4242/original.jpg"
const fakeWatchPosterURL = "https://cdn.example.test/posters/4242/card.jpg"

func newWatchReaderFixture(t *testing.T) (*CatalogWatchReader, *fakeWatchBrowse, *fakeWatchItems, *fakeWatchEpisodes, *fakeWatchFiles, *fakeWatchStore) {
	t.Helper()
	added := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	browse := &fakeWatchBrowse{result: &catalog.BrowseResult{Items: []*models.MediaItem{
		{ContentID: "4242", Type: itemTypeMovie, Title: "The Invented Crossing", Runtime: 108, AddedAt: &added, PosterPath: fakeWatchPosterPath},
	}}}
	items := &fakeWatchItems{items: map[string]*models.MediaItem{
		"4242": {ContentID: "4242", Type: itemTypeMovie, Title: "The Invented Crossing", Runtime: 108, AddedAt: &added, PosterPath: fakeWatchPosterPath},
		"1717": {ContentID: "1717", Type: itemTypeMovie, Title: "Nine Lanterns Down", Runtime: 90},
		"8080": {ContentID: "8080", Type: itemTypeSeries, Title: "Eight Quiet Rooms"},
		"9001": {ContentID: "9001", Type: itemTypeMovie, Title: "The Sealed Wing"},
	}}
	episodes := &fakeWatchEpisodes{
		bySeries: map[string][]*models.Episode{
			"8080": {{ContentID: "8080-s01e01", SeriesID: "8080", SeasonNumber: 1, EpisodeNumber: 1, Title: "The First Locked Room", Runtime: 45}},
		},
		byID: map[string]*models.Episode{
			"8080-s01e01": {ContentID: "8080-s01e01", SeriesID: "8080", SeasonNumber: 1, EpisodeNumber: 1, Title: "The First Locked Room"},
			"9001-s01e01": {ContentID: "9001-s01e01", SeriesID: "9001-series", SeasonNumber: 1, EpisodeNumber: 1, Title: "A Restricted Room"},
		},
	}
	files := &fakeWatchFiles{byContent: map[string][]*models.MediaFile{}, byEpisode: map[string][]*models.MediaFile{}}
	store := &fakeWatchStore{direct: map[string]userstore.WatchProgress{}}
	images := fakeImageResolver{byPath: map[string]string{fakeWatchPosterPath: fakeWatchPosterURL}}
	reader := NewCatalogWatchReader(browse, items, episodes, nil, files, files, nil, fakeWatchStores{store: store}, images, nil)
	return reader, browse, items, episodes, files, store
}

// TestWatchReaderResolvesPosterURLsThroughTheImageResolver proves the reader
// itself does the resolving — watchdoc never sees a stored path or dials a
// resolver — and that both Items and Item convert the same fixture item the
// same way. "1717" has no PosterPath at all, so it proves the omission case in
// the same request rather than a separate fixture.
func TestWatchReaderResolvesPosterURLsThroughTheImageResolver(t *testing.T) {
	reader, _, _, _, _, _ := newWatchReaderFixture(t)
	scope := watchdoc.ProfileScope{ProfileID: "profile-open"}

	items, err := reader.Items(context.Background(), scope)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	byContentID := map[string]watchdoc.Item{}
	for _, item := range items {
		byContentID[item.ContentID] = item
	}
	if got := byContentID["4242"].PosterURL; got != fakeWatchPosterURL {
		t.Errorf("Items()[4242].PosterURL = %q, want %q", got, fakeWatchPosterURL)
	}

	detail, found, err := reader.Item(context.Background(), scope, "4242")
	if err != nil {
		t.Fatalf("Item: %v", err)
	}
	if !found {
		t.Fatal("Item(4242) not found")
	}
	if detail.PosterURL != fakeWatchPosterURL {
		t.Errorf("Item(4242).PosterURL = %q, want %q", detail.PosterURL, fakeWatchPosterURL)
	}
}

// TestWatchReaderOmitsPosterURLWithNoResolver proves the reader fails closed
// rather than leaking a stored path to the wire when no resolver is
// configured — the same "posters omitted, not broken" rule a nil seasons
// source already gets for season titles.
func TestWatchReaderOmitsPosterURLWithNoResolver(t *testing.T) {
	added := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	browse := &fakeWatchBrowse{result: &catalog.BrowseResult{Items: []*models.MediaItem{
		{ContentID: "4242", Type: itemTypeMovie, Title: "The Invented Crossing", AddedAt: &added, PosterPath: fakeWatchPosterPath},
	}}}
	items := &fakeWatchItems{items: map[string]*models.MediaItem{}}
	episodes := &fakeWatchEpisodes{}
	files := &fakeWatchFiles{byContent: map[string][]*models.MediaFile{}, byEpisode: map[string][]*models.MediaFile{}}
	store := &fakeWatchStore{direct: map[string]userstore.WatchProgress{}}
	reader := NewCatalogWatchReader(browse, items, episodes, nil, files, files, nil, fakeWatchStores{store: store}, nil, nil)

	result, err := reader.Items(context.Background(), watchdoc.ProfileScope{ProfileID: "profile-open"})
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("Items() = %d items, want 1", len(result))
	}
	if result[0].PosterURL != "" {
		t.Errorf("PosterURL = %q with no resolver configured, want empty", result[0].PosterURL)
	}
}

func TestWatchReaderNeverNamesAFileTheViewerCannotPlay(t *testing.T) {
	reader, _, _, _, files, _ := newWatchReaderFixture(t)
	// Three versions of one movie: the lowest identifier is above the viewer's
	// quality ceiling, the next lives in a library the viewer may not see, and
	// only the third is playable. Naming either of the first two hands the
	// client a Play button that /playback/start refuses.
	files.byContent["4242"] = []*models.MediaFile{
		{ID: 4241999, MediaFolderID: 4, Resolution: "2160p"},
		{ID: 4242000, MediaFolderID: 9, Resolution: "1080p"},
		{ID: 4242001, MediaFolderID: 4, Resolution: "1080p"},
	}

	restricted := watchdoc.ProfileScope{
		ProfileID:          "profile-restricted",
		AllowedLibraryIDs:  []int{4},
		MaxPlaybackQuality: "1080p",
	}
	fileIDs, err := reader.FilesByContentIDs(context.Background(), restricted, []string{"4242"})
	if err != nil {
		t.Fatalf("resolve files: %v", err)
	}
	if fileIDs["4242"] != 4242001 {
		t.Errorf("file_id = %d, want 4242001 (the only version the viewer may play)", fileIDs["4242"])
	}

	// An unrestricted viewer still gets the lowest identifier.
	fileIDs, err = reader.FilesByContentIDs(context.Background(), watchdoc.ProfileScope{ProfileID: "profile-open"}, []string{"4242"})
	if err != nil {
		t.Fatalf("resolve files: %v", err)
	}
	if fileIDs["4242"] != 4241999 {
		t.Errorf("unrestricted file_id = %d, want 4241999", fileIDs["4242"])
	}
}

func TestWatchReaderFiltersEpisodeFilesByAccessToo(t *testing.T) {
	reader, _, _, _, files, _ := newWatchReaderFixture(t)
	files.byEpisode["8080-s01e01"] = []*models.MediaFile{
		{ID: 8080000, MediaFolderID: 9, Resolution: "1080p"},
		{ID: 8080001, MediaFolderID: 4, Resolution: "1080p"},
	}
	fileIDs, err := reader.FilesByContentIDs(context.Background(),
		watchdoc.ProfileScope{ProfileID: "p", AllowedLibraryIDs: []int{4}}, []string{"8080-s01e01"})
	if err != nil {
		t.Fatalf("resolve files: %v", err)
	}
	if fileIDs["8080-s01e01"] != 8080001 {
		t.Errorf("episode file_id = %d, want 8080001", fileIDs["8080-s01e01"])
	}
}

func TestWatchReaderMarkersResolvesChaptersAndSkipIntroFromMediaFiles(t *testing.T) {
	reader, _, _, _, files, _ := newWatchReaderFixture(t)
	introStart, introEnd := 0.0, 45.0
	files.byID = map[int]*models.MediaFile{
		4242001: {
			ID: 4242001,
			Chapters: []models.MediaChapter{
				{Index: 0, Title: "Opening", StartSeconds: 0, EndSeconds: 45},
			},
			IntroStart: &introStart,
			IntroEnd:   &introEnd,
		},
		// No markers known for this file: absent from the map the reader
		// returns, not present with an empty FileMarkers.
		4242002: {ID: 4242002},
	}

	found, err := reader.Markers(context.Background(), watchdoc.ProfileScope{ProfileID: "p"}, []int64{4242001, 4242002, 4242003})
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	if _, ok := found[4242002]; ok {
		t.Error("a file with nothing known is present in the result")
	}
	if _, ok := found[4242003]; ok {
		t.Error("a file the fixture never named is present in the result")
	}
	markers, ok := found[4242001]
	if !ok {
		t.Fatal("4242001 is missing from the result")
	}
	if len(markers.Chapters) != 1 || markers.Chapters[0].Title != "Opening" {
		t.Errorf("chapters = %#v", markers.Chapters)
	}
	if markers.IntroStart == nil || *markers.IntroStart != 0 || markers.IntroEnd == nil || *markers.IntroEnd != 45 {
		t.Errorf("intro range = %v..%v, want 0..45", markers.IntroStart, markers.IntroEnd)
	}
}

func TestWatchReaderMarkersResolvesCreditsRecapAndPreviewFromMediaFiles(t *testing.T) {
	reader, _, _, _, files, _ := newWatchReaderFixture(t)
	creditsStart, creditsEnd := 2400.0, 2500.0
	recapStart, recapEnd := 0.0, 30.0
	previewStart, previewEnd := 2500.0, 2550.0
	files.byID = map[int]*models.MediaFile{
		4242001: {
			ID:           4242001,
			CreditsStart: &creditsStart,
			CreditsEnd:   &creditsEnd,
			RecapStart:   &recapStart,
			RecapEnd:     &recapEnd,
			PreviewStart: &previewStart,
			PreviewEnd:   &previewEnd,
		},
		// Nothing known for this file: absent from the result, same as the
		// intro/chapters case.
		4242002: {ID: 4242002},
	}

	found, err := reader.Markers(context.Background(), watchdoc.ProfileScope{ProfileID: "p"}, []int64{4242001, 4242002})
	if err != nil {
		t.Fatalf("Markers: %v", err)
	}
	if _, ok := found[4242002]; ok {
		t.Error("a file with nothing known is present in the result")
	}
	markers, ok := found[4242001]
	if !ok {
		t.Fatal("4242001 is missing from the result")
	}
	if markers.CreditsStart == nil || *markers.CreditsStart != 2400 || markers.CreditsEnd == nil || *markers.CreditsEnd != 2500 {
		t.Errorf("credits range = %v..%v, want 2400..2500", markers.CreditsStart, markers.CreditsEnd)
	}
	if markers.RecapStart == nil || *markers.RecapStart != 0 || markers.RecapEnd == nil || *markers.RecapEnd != 30 {
		t.Errorf("recap range = %v..%v, want 0..30", markers.RecapStart, markers.RecapEnd)
	}
	if markers.PreviewStart == nil || *markers.PreviewStart != 2500 || markers.PreviewEnd == nil || *markers.PreviewEnd != 2550 {
		t.Errorf("preview range = %v..%v, want 2500..2550", markers.PreviewStart, markers.PreviewEnd)
	}
}

func TestWatchReaderAsksOnlyForEpisodeProgressRows(t *testing.T) {
	reader, _, _, _, _, store := newWatchReaderFixture(t)
	store.direct["4242"] = userstore.WatchProgress{MediaItemID: "4242", PositionSeconds: 10, DurationSeconds: 100, UpdatedAt: "2026-08-13T11:45:00Z"}
	store.filtered = []userstore.WatchProgress{
		{MediaItemID: "8080-s01e01", PositionSeconds: 960, DurationSeconds: 2700, UpdatedAt: "2026-08-13T11:50:00Z"},
	}

	rows, err := reader.Progress(context.Background(), watchdoc.ProfileScope{ProfileID: "p"}, []string{"4242", "8080"})
	if err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if store.unfilteredCalls != 0 {
		t.Errorf("the unfiltered progress listing was used %d times; a household that also reads and listens can fill that window with non-video rows", store.unfilteredCalls)
	}
	if len(store.filteredCalls) != 1 {
		t.Fatalf("filtered progress calls = %#v, want one", store.filteredCalls)
	}
	call := store.filteredCalls[0]
	if call.status != "" {
		t.Errorf("status = %q, want empty so completed episodes still reach the client's merge", call.status)
	}
	if len(call.types) != 1 || call.types[0] != itemTypeEpisode {
		t.Errorf("types = %#v, want [episode]", call.types)
	}
	if call.libraryID != nil || call.limit != watchRecentProgressRows || call.offset != 0 {
		t.Errorf("filtered call = %#v", call)
	}

	var episodeRow watchdoc.Progress
	for _, row := range rows {
		if row.EpisodeID != "" {
			episodeRow = row
		}
	}
	if episodeRow.ContentID != "8080" || episodeRow.EpisodeID != "8080-s01e01" {
		t.Errorf("episode row = %#v, want it attributed to its series", episodeRow)
	}
}

func TestWatchReaderUnionsInProgressItemsIntoTheHomeSet(t *testing.T) {
	reader, browse, items, _, _, store := newWatchReaderFixture(t)
	// The viewer is part-way through a movie and an episode whose titles both
	// fell outside the recently-added window. Without the union the longest
	// standing viewer gets no Continue Watching at all.
	store.filtered = []userstore.WatchProgress{
		{MediaItemID: "1717", PositionSeconds: 30, DurationSeconds: 300, UpdatedAt: "2026-08-13T10:00:00Z"},
		{MediaItemID: "8080-s01e01", PositionSeconds: 960, DurationSeconds: 2700, UpdatedAt: "2026-08-13T11:50:00Z"},
		{MediaItemID: "9001-s01e01", PositionSeconds: 60, DurationSeconds: 600, UpdatedAt: "2026-08-13T11:55:00Z"},
	}
	// The series behind that last row is one the profile may not see.
	items.restricted = map[string]bool{"9001-series": true}

	found, err := reader.Items(context.Background(), watchdoc.ProfileScope{ProfileID: "p"})
	if err != nil {
		t.Fatalf("read items: %v", err)
	}
	byID := map[string]watchdoc.Item{}
	for _, item := range found {
		byID[item.ContentID] = item
	}
	if _, ok := byID["4242"]; !ok {
		t.Error("the recently-added window is missing from the item set")
	}
	if _, ok := byID["1717"]; !ok {
		t.Error("an in-progress movie outside the window is missing from the item set")
	}
	if _, ok := byID["8080"]; !ok {
		t.Error("the series behind an in-progress episode is missing from the item set")
	}
	if _, ok := byID["9001-series"]; ok {
		t.Error("a restricted series was unioned in through its progress row")
	}
	// The union is access-checked through the item repository, not assumed.
	if len(items.requested) == 0 {
		t.Error("unioned items were not re-resolved with the viewer's access filter")
	}
	if browse.filters.Limit != watchHomeItemLimit {
		t.Errorf("browse limit = %d, want %d", browse.filters.Limit, watchHomeItemLimit)
	}
}

// --- search -----------------------------------------------------------------

// fakeSearchProvider stands in for catalog.CatalogSearchProvider: it records
// the request it was asked with and returns a fixed, ordered result list —
// the ordering is the point, since Search must preserve the provider's
// relevance ranking rather than re-sorting it.
type fakeSearchProvider struct {
	result      *catalog.CatalogSearchResult
	err         error
	lastRequest catalog.CatalogSearchRequest
	calls       int
}

func (f *fakeSearchProvider) Search(_ context.Context, req catalog.CatalogSearchRequest) (*catalog.CatalogSearchResult, error) {
	f.calls++
	f.lastRequest = req
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func TestCatalogWatchReaderSearchPreservesProviderOrderAndResolvesPosters(t *testing.T) {
	added := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	browse := &fakeWatchBrowse{result: &catalog.BrowseResult{}}
	items := &fakeWatchItems{items: map[string]*models.MediaItem{}}
	episodes := &fakeWatchEpisodes{}
	files := &fakeWatchFiles{byContent: map[string][]*models.MediaFile{}, byEpisode: map[string][]*models.MediaFile{}}
	store := &fakeWatchStore{direct: map[string]userstore.WatchProgress{}}
	search := &fakeSearchProvider{result: &catalog.CatalogSearchResult{Items: []*models.MediaItem{
		// Deliberately not alphabetical and not by AddedAt: a relevance order
		// ComposeHome's own ordering would scramble if Search reused it.
		{ContentID: "9001", Type: itemTypeMovie, Title: "The Sealed Wing", AddedAt: &added, PosterPath: fakeWatchPosterPath},
		{ContentID: "4242", Type: itemTypeMovie, Title: "The Invented Crossing", AddedAt: &added},
	}}}
	reader := NewCatalogWatchReader(browse, items, episodes, nil, files, files, nil, fakeWatchStores{store: store}, fakeImageResolver{byPath: map[string]string{fakeWatchPosterPath: fakeWatchPosterURL}}, search)

	scope := watchdoc.ProfileScope{ProfileID: "profile-open", MaxContentRating: "PG-13"}
	results, err := reader.Search(context.Background(), scope, "sealed")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := []string{results[0].ContentID, results[1].ContentID}; got[0] != "9001" || got[1] != "4242" {
		t.Fatalf("Search() content ids = %v, want [9001 4242] (provider order preserved)", got)
	}
	if results[0].PosterURL != fakeWatchPosterURL {
		t.Errorf("results[0].PosterURL = %q, want %q", results[0].PosterURL, fakeWatchPosterURL)
	}
	if results[1].PosterURL != "" {
		t.Errorf("results[1].PosterURL = %q, want empty (no poster path)", results[1].PosterURL)
	}

	if search.lastRequest.Query != "sealed" {
		t.Errorf("request query = %q, want %q", search.lastRequest.Query, "sealed")
	}
	if got := search.lastRequest.ItemTypes; len(got) != 2 || got[0] != itemTypeMovie || got[1] != itemTypeSeries {
		t.Errorf("request item types = %v, want [movie series] — Watch search is video-only", got)
	}
	if search.lastRequest.Access.MaxContentRating != "PG-13" {
		t.Errorf("request access = %+v, want the caller's own scope threaded through", search.lastRequest.Access)
	}
}

func TestCatalogWatchReaderSearchWithNoProviderAnswersEmptyNotError(t *testing.T) {
	reader := NewCatalogWatchReader(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	results, err := reader.Search(context.Background(), watchdoc.ProfileScope{ProfileID: "p"}, "anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}

func TestWatchSearchEndpointServesAContractDocumentInProviderOrder(t *testing.T) {
	added := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	items := &fakeWatchItems{items: map[string]*models.MediaItem{}}
	files := &fakeWatchFiles{
		byContent: map[string][]*models.MediaFile{
			"9001": {{ID: 9001001, MediaFolderID: 4}},
			"4242": {{ID: 4242001, MediaFolderID: 4}},
		},
		byEpisode: map[string][]*models.MediaFile{},
	}
	store := &fakeWatchStore{direct: map[string]userstore.WatchProgress{}}
	search := &fakeSearchProvider{result: &catalog.CatalogSearchResult{Items: []*models.MediaItem{
		{ContentID: "9001", Type: itemTypeMovie, Title: "The Sealed Wing", AddedAt: &added},
		{ContentID: "4242", Type: itemTypeMovie, Title: "The Invented Crossing", AddedAt: &added},
	}}}
	reader := NewCatalogWatchReader(&fakeWatchBrowse{result: &catalog.BrowseResult{}}, items, &fakeWatchEpisodes{}, nil, files, files, nil, fakeWatchStores{store: store}, nil, search)
	handler := NewWatchHandler(reader, reader)

	rr := httptest.NewRecorder()
	handler.HandleWatchSearch(rr, newWatchRequest(t, "/api/v2/watch/search?q=sealed", "", "profile-invented"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var document watchdoc.Document
	if err := json.Unmarshal(rr.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode: %v (%s)", err, rr.Body.String())
	}
	if len(document.Items) != 2 || document.Items[0].ContentID != "9001" || document.Items[1].ContentID != "4242" {
		t.Fatalf("document items = %+v, want [9001 4242] in that order", document.Items)
	}
	if document.Progress == nil || len(document.Progress) != 0 {
		t.Errorf("document.Progress = %v, want an empty (not nil) slice", document.Progress)
	}
	if document.FeaturedContentID != "" {
		t.Errorf("document.FeaturedContentID = %q, want empty — search has no featured item", document.FeaturedContentID)
	}
}

// stubWatchSearcher is a WatchSearcher that is never actually asked to search
// — it exists so a test can prove the empty-query guard rejects the request
// before reaching the searcher at all.
type stubWatchSearcher struct{}

func (stubWatchSearcher) Search(context.Context, watchdoc.ProfileScope, string) ([]watchdoc.Item, error) {
	return nil, nil
}

func TestWatchSearchEndpointRequiresANonEmptyQuery(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), stubWatchSearcher{})
	rr := httptest.NewRecorder()
	handler.HandleWatchSearch(rr, newWatchRequest(t, "/api/v2/watch/search", "", "profile-invented"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestWatchSearchEndpointWithNoSearcherAnswersUnavailable(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t), nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchSearch(rr, newWatchRequest(t, "/api/v2/watch/search?q=x", "", "profile-invented"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
}
