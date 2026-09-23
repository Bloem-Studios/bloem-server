package handlers

// Wire-pinning tests Bloem added for the requests handlers, moved out of Silo's
// requests_test.go so that file stays byte-identical to upstream.

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

	mediarequests "github.com/Silo-Server/silo-server/internal/requests"
)

// bloemRequestService extends Silo's fakeRequestService with the knobs these
// tests need, overriding only the methods they drive.
type bloemRequestService struct {
	*fakeRequestService
	discoverAllFn func() ([]mediarequests.DiscoverySection, error)
	listMineFn    func() ([]*mediarequests.Request, error)
	createErr     error
	cancelReason  string
}

func (f *bloemRequestService) DiscoverAll(context.Context, mediarequests.Viewer) ([]mediarequests.DiscoverySection, error) {
	if f.discoverAllFn != nil {
		return f.discoverAllFn()
	}
	return nil, nil
}

func (f *bloemRequestService) CreateRequest(context.Context, mediarequests.Viewer, mediarequests.CreateRequestInput) (*mediarequests.Request, error) {
	return nil, f.createErr
}

func (f *bloemRequestService) ListMine(context.Context, mediarequests.Viewer, mediarequests.ListFilter) ([]*mediarequests.Request, error) {
	if f.listMineFn != nil {
		return f.listMineFn()
	}
	return nil, nil
}

func (f *bloemRequestService) Cancel(_ context.Context, _ mediarequests.Viewer, _ string, reason string) (*mediarequests.Request, error) {
	f.cancelReason = reason
	return nil, nil
}

// TestHandleDiscoverPinsWire pins the GET /requests/discover body: the named
// discoverSectionsResponse replaced an inline struct literal, and this test
// proves the marshaled bytes did not change — same field names, same tags,
// same omitempty, same order of population.
func TestHandleDiscoverPinsWire(t *testing.T) {
	svc := &bloemRequestService{fakeRequestService: &fakeRequestService{}, discoverAllFn: func() ([]mediarequests.DiscoverySection, error) {
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
// this test proves the marshaled bytes did not change — same field names,
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
// and this test proves the marshaled bytes did not change — same field
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
// this test proves the marshaled bytes did not change — same field names,
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
// marshaled bytes did not change — same field names, same tags, same
// omitempty, same order of population.
func TestHandleListMinePinsWire(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := &bloemRequestService{fakeRequestService: &fakeRequestService{}, listMineFn: func() ([]*mediarequests.Request, error) {
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
// and this test proves the marshaled bytes did not change — same keys, same
// values, and the map's sorted key order reproduced by the field order.
func TestHandleCreateValidationErrorResponsePinsWire(t *testing.T) {
	svc := &bloemRequestService{fakeRequestService: &fakeRequestService{}}
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
// proves the marshaled bytes did not change — same field names, same tags,
// same order of population.
func TestHandleCreateQuotaErrorResponsePinsWire(t *testing.T) {
	svc := &bloemRequestService{fakeRequestService: &fakeRequestService{}}
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

// TestHandleCancelReasonBodyDecodes pins the POST /requests/{id}/cancel
// request body: the named requestReasonRequest (shared with the admin decline
// endpoint) replaced an inline struct literal, and this test proves the wire
// contract did not change — the client still sends {"reason":"…"} and the
// handler still passes the value through to the service.
func TestHandleCancelReasonBodyDecodes(t *testing.T) {
	svc := &bloemRequestService{fakeRequestService: &fakeRequestService{}}
	h := NewRequestsHandler(svc)

	req := authedRequest("POST", "/api/v1/requests/req-9/cancel")
	req.Body = io.NopCloser(strings.NewReader(`{"reason":"wrong movie"}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "req-9")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.HandleCancel(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if svc.cancelReason != "wrong movie" {
		t.Fatalf("reason = %q, want %q", svc.cancelReason, "wrong movie")
	}
	var wire struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(`{"reason":"wrong movie"}`), &wire); err != nil || wire.Reason != "wrong movie" {
		t.Fatalf("wire name changed: %v %+v", err, wire)
	}
}
