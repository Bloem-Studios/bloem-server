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

func (r *watchTestReader) FilesByContentIDs(_ context.Context, contentIDs []string) (map[string]int64, error) {
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
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v1/watch/home", "", "profile-invented"))

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
	handler := NewWatchHandler(reader)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v1/watch/home", "", "profile-restricted"))

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
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v1/watch/home", "", ""))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "profile_required" {
		t.Errorf("error = %#v, want profile_required", body["error"])
	}
}

func TestWatchItemEndpointServesASeriesDetailDocument(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v1/watch/items/8080", "8080", "profile-invented"))

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
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v1/watch/items/8080", "8080", ""))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body["error"] != "profile_required" {
		t.Errorf("error = %#v, want profile_required", body["error"])
	}
}

func TestWatchItemEndpointAnswersNotFoundForAnUnknownContentID(t *testing.T) {
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v1/watch/items/3030", "3030", "profile-invented"))

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
	handler := NewWatchHandler(newWatchTestReader(t))
	rr := httptest.NewRecorder()
	handler.HandleWatchItem(rr, newWatchRequest(t, "/api/v1/watch/items/", "", "profile-invented"))

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestWatchEndpointsReportAFailingReader(t *testing.T) {
	reader := newWatchTestReader(t)
	reader.err = errors.New("catalog unavailable")
	handler := NewWatchHandler(reader)

	home := httptest.NewRecorder()
	handler.HandleWatchHome(home, newWatchRequest(t, "/api/v1/watch/home", "", "profile-invented"))
	if home.Code != http.StatusInternalServerError {
		t.Errorf("home status = %d, want 500; body %s", home.Code, home.Body.String())
	}

	detail := httptest.NewRecorder()
	handler.HandleWatchItem(detail, newWatchRequest(t, "/api/v1/watch/items/4242", "4242", "profile-invented"))
	if detail.Code != http.StatusInternalServerError {
		t.Errorf("detail status = %d, want 500; body %s", detail.Code, detail.Body.String())
	}
}

func TestWatchEndpointsWithoutAReaderAreUnavailable(t *testing.T) {
	handler := NewWatchHandler(nil)
	rr := httptest.NewRecorder()
	handler.HandleWatchHome(rr, newWatchRequest(t, "/api/v1/watch/home", "", "profile-invented"))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", rr.Code, rr.Body.String())
	}
}
