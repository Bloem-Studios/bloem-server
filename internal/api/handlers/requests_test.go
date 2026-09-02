package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

type fakeRequestService struct {
	listStudiosFn  func() ([]mediarequests.DiscoverBrandCard, error)
	listNetworksFn func() ([]mediarequests.DiscoverBrandCard, error)
	listGenresFn   func() ([]mediarequests.DiscoverBrandCard, error)
	browseFn       func(kind, slug string, mediaType mediarequests.MediaType, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error)
	discoverAllFn  func() ([]mediarequests.DiscoverySection, error)
	listMineFn     func() ([]*mediarequests.Request, error)
	createErr      error
}

func (f *fakeRequestService) ListStudios(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listStudiosFn != nil {
		return f.listStudiosFn()
	}
	return nil, nil
}

func (f *fakeRequestService) ListNetworks(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listNetworksFn != nil {
		return f.listNetworksFn()
	}
	return nil, nil
}

func (f *fakeRequestService) ListGenres(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverBrandCard, error) {
	if f.listGenresFn != nil {
		return f.listGenresFn()
	}
	return nil, nil
}

func (f *fakeRequestService) BrowseStudio(_ context.Context, _ mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("studio", slug, mediarequests.MediaTypeMovie, sort, page)
}

func (f *fakeRequestService) BrowseNetwork(_ context.Context, _ mediarequests.Viewer, slug, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("network", slug, mediarequests.MediaTypeSeries, sort, page)
}

func (f *fakeRequestService) BrowseGenre(_ context.Context, _ mediarequests.Viewer, slug string, mediaType mediarequests.MediaType, sort string, page int) (*mediarequests.DiscoverBrowseResponse, error) {
	return f.browseFn("genre", slug, mediaType, sort, page)
}

func (f *fakeRequestService) Search(context.Context, mediarequests.Viewer, string, mediarequests.MediaType, int) (*mediarequests.MediaPage, error) {
	return nil, nil
}

func (f *fakeRequestService) Discover(context.Context, mediarequests.Viewer, string, int) (*mediarequests.DiscoverySection, error) {
	return nil, nil
}

func (f *fakeRequestService) DiscoverAll(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverySection, error) {
	if f.discoverAllFn != nil {
		return f.discoverAllFn()
	}
	return nil, nil
}

func (f *fakeRequestService) GetDetail(context.Context, mediarequests.Viewer, mediarequests.MediaType, int) (*mediarequests.MediaDetail, error) {
	return nil, nil
}

func (f *fakeRequestService) CreateRequest(context.Context, mediarequests.Viewer, mediarequests.CreateRequestInput) (*mediarequests.Request, error) {
	return nil, f.createErr
}

func (f *fakeRequestService) ListMine(context.Context, mediarequests.Viewer, mediarequests.ListFilter) ([]*mediarequests.Request, error) {
	if f.listMineFn != nil {
		return f.listMineFn()
	}
	return nil, nil
}

func (f *fakeRequestService) ListAdmin(context.Context, mediarequests.Viewer, mediarequests.ListFilter) ([]*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) GetRequest(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Approve(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Decline(context.Context, mediarequests.Viewer, string, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Cancel(context.Context, mediarequests.Viewer, string, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) Retry(context.Context, mediarequests.Viewer, string) (*mediarequests.Request, error) {
	return nil, nil
}

func (f *fakeRequestService) GetFeatureStatus(context.Context, mediarequests.Viewer) (mediarequests.FeatureStatus, error) {
	return mediarequests.FeatureStatus{}, nil
}

func (f *fakeRequestService) GetSettings(context.Context, mediarequests.Viewer) (mediarequests.Settings, error) {
	return mediarequests.Settings{}, nil
}

func (f *fakeRequestService) UpdateSettings(context.Context, mediarequests.Viewer, mediarequests.Settings) (mediarequests.Settings, error) {
	return mediarequests.Settings{}, nil
}

func (f *fakeRequestService) GetUserLimit(context.Context, mediarequests.Viewer, int) (*mediarequests.UserLimit, error) {
	return nil, nil
}

func (f *fakeRequestService) UpsertUserLimit(context.Context, mediarequests.Viewer, mediarequests.UserLimit) (*mediarequests.UserLimit, error) {
	return nil, nil
}

func (f *fakeRequestService) ListIntegrations(context.Context, mediarequests.Viewer) ([]mediarequests.Integration, error) {
	return nil, nil
}

func (f *fakeRequestService) CreateIntegration(_ context.Context, _ mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error) {
	return &integration, nil
}

func (f *fakeRequestService) UpdateIntegration(_ context.Context, _ mediarequests.Viewer, integration mediarequests.Integration) (*mediarequests.Integration, error) {
	return &integration, nil
}

func (f *fakeRequestService) DeleteIntegration(context.Context, mediarequests.Viewer, string) error {
	return nil
}

func (f *fakeRequestService) LoadIntegrationOptions(context.Context, mediarequests.Viewer, mediarequests.Integration) (map[string][]mediarequests.RouterOption, error) {
	return nil, nil
}

func authedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{
		UserID:    1,
		Role:      "user",
		TokenType: auth.TokenTypeAccess,
	})
	ctx = apimw.SetProfileID(ctx, "profile-1")
	return req.WithContext(ctx)
}

func TestHandleListStudiosReturnsJSON(t *testing.T) {
	logo := "https://image.tmdb.org/t/p/w300/x.png"
	svc := &fakeRequestService{
		listStudiosFn: func() ([]mediarequests.DiscoverBrandCard, error) {
			return []mediarequests.DiscoverBrandCard{
				{TMDBID: 420, Slug: "marvel-studios", DisplayName: "Marvel Studios", LogoURL: &logo},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleListStudios(rec, authedRequest("GET", "/api/v1/requests/discover/studios"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Studios []mediarequests.DiscoverBrandCard `json:"studios"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Studios) != 1 || body.Studios[0].Slug != "marvel-studios" {
		t.Errorf("studios = %+v", body.Studios)
	}
}

func TestHandleBrowseStudioRejectsUnknownSort(t *testing.T) {
	svc := &fakeRequestService{
		browseFn: func(kind, slug string, _ mediarequests.MediaType, sort string, _ int) (*mediarequests.DiscoverBrowseResponse, error) {
			return nil, mediarequests.ErrInvalidInput
		},
	}
	h := NewRequestsHandler(svc)

	req := authedRequest("GET", "/api/v1/requests/discover/browse/studio/marvel-studios?sort=garbage")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "marvel-studios")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.HandleBrowseStudio(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleBrowseStudioUnknownSlugReturns404(t *testing.T) {
	svc := &fakeRequestService{
		browseFn: func(string, string, mediarequests.MediaType, string, int) (*mediarequests.DiscoverBrowseResponse, error) {
			return nil, mediarequests.ErrNotFound
		},
	}
	h := NewRequestsHandler(svc)

	req := authedRequest("GET", "/api/v1/requests/discover/browse/studio/ghosts")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "ghosts")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.HandleBrowseStudio(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleBrowseGenreRequiresMediaType(t *testing.T) {
	svc := &fakeRequestService{
		browseFn: func(_ string, _ string, mt mediarequests.MediaType, _ string, _ int) (*mediarequests.DiscoverBrowseResponse, error) {
			if strings.TrimSpace(string(mt)) == "" {
				return nil, mediarequests.ErrInvalidInput
			}
			return &mediarequests.DiscoverBrowseResponse{Kind: "genre"}, nil
		},
	}
	h := NewRequestsHandler(svc)

	req := authedRequest("GET", "/api/v1/requests/discover/browse/genre/action")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("slug", "action")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.HandleBrowseGenre(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleDiscoverPinsWire pins the GET /requests/discover body: the named
// discoverSectionsResponse replaced an inline struct literal, and this test
// proves the marshalled bytes did not change — same field names, same tags,
// same omitempty, same order of population.
func TestHandleDiscoverPinsWire(t *testing.T) {
	svc := &fakeRequestService{
		discoverAllFn: func() ([]mediarequests.DiscoverySection, error) {
			return []mediarequests.DiscoverySection{
				{Key: "popular", Title: "Popular", Page: 1, Results: []mediarequests.MediaResult(nil)},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleDiscover(rec, authedRequest("GET", "/api/v1/requests/discover"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"sections":[{"key":"popular","title":"Popular","page":1,"total_pages":0,"total_results":0,"results":null}]}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleListStudiosPinsWire pins the GET /requests/discover/studios body:
// the named discoverStudiosResponse replaced an inline struct literal, and
// this test proves the marshalled bytes did not change — same field names,
// same tags, same omitempty, same order of population.
func TestHandleListStudiosPinsWire(t *testing.T) {
	logo := "https://image.tmdb.org/t/p/w300/x.png"
	svc := &fakeRequestService{
		listStudiosFn: func() ([]mediarequests.DiscoverBrandCard, error) {
			return []mediarequests.DiscoverBrandCard{
				{TMDBID: 420, Slug: "marvel-studios", DisplayName: "Marvel Studios", LogoURL: &logo},
				{Slug: "a24", DisplayName: "A24", SeriesSupported: true},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleListStudios(rec, authedRequest("GET", "/api/v1/requests/discover/studios"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"studios":[` +
		`{"tmdb_id":420,"slug":"marvel-studios","display_name":"Marvel Studios","logo_url":"https://image.tmdb.org/t/p/w300/x.png"},` +
		`{"slug":"a24","display_name":"A24","series_supported":true}` +
		`]}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleListNetworksPinsWire pins the GET /requests/discover/networks
// body: the named discoverNetworksResponse replaced an inline struct literal,
// and this test proves the marshalled bytes did not change — same field
// names, same tags, same omitempty, same order of population.
func TestHandleListNetworksPinsWire(t *testing.T) {
	svc := &fakeRequestService{
		listNetworksFn: func() ([]mediarequests.DiscoverBrandCard, error) {
			return []mediarequests.DiscoverBrandCard{
				{TMDBID: 2134, Slug: "netflix", DisplayName: "Netflix"},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleListNetworks(rec, authedRequest("GET", "/api/v1/requests/discover/networks"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"networks":[{"tmdb_id":2134,"slug":"netflix","display_name":"Netflix"}]}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleListGenresPinsWire pins the GET /requests/discover/genres body:
// the named discoverGenresResponse replaced an inline struct literal, and
// this test proves the marshalled bytes did not change — same field names,
// same tags, same omitempty, same order of population.
func TestHandleListGenresPinsWire(t *testing.T) {
	svc := &fakeRequestService{
		listGenresFn: func() ([]mediarequests.DiscoverBrandCard, error) {
			return []mediarequests.DiscoverBrandCard{
				{TMDBID: 28, Slug: "action", DisplayName: "Action"},
				{Slug: "documentary", DisplayName: "Documentary"},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleListGenres(rec, authedRequest("GET", "/api/v1/requests/discover/genres"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"genres":[` +
		`{"tmdb_id":28,"slug":"action","display_name":"Action"},` +
		`{"slug":"documentary","display_name":"Documentary"}` +
		`]}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleListMinePinsWire pins the GET /requests/mine body: the named
// requestListResponse (shared with the admin list endpoint, which writes the
// same shape) replaced an inline struct literal, and this test proves the
// marshalled bytes did not change — same field names, same tags, same
// omitempty, same order of population.
func TestHandleListMinePinsWire(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &fakeRequestService{
		listMineFn: func() ([]*mediarequests.Request, error) {
			return []*mediarequests.Request{
				{
					ID:        "req-1",
					Provider:  "tmdb",
					MediaType: mediarequests.MediaTypeMovie,
					TMDBID:    42,
					Title:     "Arrival",
					CreatedAt: created,
					UpdatedAt: created,
				},
			}, nil
		},
	}
	h := NewRequestsHandler(svc)

	rec := httptest.NewRecorder()
	h.HandleListMine(rec, authedRequest("GET", "/api/v1/requests/mine"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"requests":[{"id":"req-1","provider":"tmdb","media_type":"movie","tmdb_id":42,"title":"Arrival","status":"","outcome":"","is_anime":false,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}]}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleCreateValidationErrorResponsePinsWire pins the 400 body
// writeRequestServiceError writes for a validation failure: the named
// requestValidationErrorResponse replaced an inline map[string]any literal,
// and this test proves the marshalled bytes did not change — same keys, same
// values, and the map's sorted key order reproduced by the field order.
func TestHandleCreateValidationErrorResponsePinsWire(t *testing.T) {
	svc := &fakeRequestService{}
	svc.createErr = &mediarequests.ValidationError{
		FieldErrors: map[string]string{"tmdb_id": "must be positive"},
		FormError:   "",
	}
	h := NewRequestsHandler(svc)

	req := authedRequest("POST", "/api/v1/requests")
	req.Body = io.NopCloser(strings.NewReader(`{"tmdb_id":0}`))

	rec := httptest.NewRecorder()
	h.HandleCreate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"error":"validation_failed","field_errors":{"tmdb_id":"must be positive"},"form_error":""}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}

// TestHandleCreateQuotaErrorResponsePinsWire pins the 429 body
// writeRequestServiceError writes when the quota is exhausted: the named
// requestQuotaErrorResponse replaced an inline struct literal, and this test
// proves the marshalled bytes did not change — same field names, same tags,
// same order of population.
func TestHandleCreateQuotaErrorResponsePinsWire(t *testing.T) {
	svc := &fakeRequestService{}
	svc.createErr = mediarequests.QuotaError{Used: 5, Limit: 5, WindowDays: 7}
	h := NewRequestsHandler(svc)

	req := authedRequest("POST", "/api/v1/requests")
	req.Body = io.NopCloser(strings.NewReader(`{"tmdb_id":42}`))

	rec := httptest.NewRecorder()
	h.HandleCreate(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"error":"quota_exceeded","message":"Request quota exceeded","used":5,"limit":5,"window_days":7}`
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != want {
		t.Fatalf("wire changed:\n got %s\nwant %s", got, want)
	}
}
