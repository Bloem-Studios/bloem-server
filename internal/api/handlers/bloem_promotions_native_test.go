package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Silo-Server/silo-server/internal/promotions"
)

// fakePromotionViewerSource adds the native routes' home delivery and
// dismissals to fakePromotionSource.
type fakePromotionViewerSource struct {
	fakePromotionSource
	homeCards    []promotions.Card
	homePosition int
	homeViewer   promotions.Viewer
	dismissed    []string
	dismissErr   error
}

func (f *fakePromotionViewerSource) ActiveHome(_ context.Context, v promotions.Viewer) ([]promotions.Card, int, error) {
	f.homeViewer = v
	return f.homeCards, f.homePosition, f.err
}

func (f *fakePromotionViewerSource) Dismiss(_ context.Context, v promotions.Viewer, surface, id string) error {
	if f.dismissErr != nil {
		return f.dismissErr
	}
	f.dismissed = append(f.dismissed, v.ProfileID+"|"+surface+"|"+id)
	return nil
}

func decodeNativePromotions(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The native list serves the home surface, with the row position.
func TestNativePromotionsListServesHomeWithItsPosition(t *testing.T) {
	src := &fakePromotionViewerSource{homeCards: []promotions.Card{promoCard("01HOME")}, homePosition: 3}
	rec := httptest.NewRecorder()
	(&PromotionsHandler{source: src}).HandleNativeList(rec, newPromotionsRequest("/promotions?surface=home"))
	body := decodeNativePromotions(t, rec)
	if string(body["surface"]) != `"home"` || string(body["home_position"]) != "3" {
		t.Fatalf("body = %v", body)
	}
	var cards []promotions.Card
	if err := json.Unmarshal(body["promotions"], &cards); err != nil || len(cards) != 1 || cards[0].ID != "01HOME" {
		t.Fatalf("promotions = %s (%v)", body["promotions"], err)
	}
	if src.homeViewer.UserID != 7 || src.homeViewer.ProfileID != "p1" {
		t.Fatalf("viewer = %+v", src.homeViewer)
	}
}

// Other surfaces answer without home_position, and an empty result is []
// rather than null.
func TestNativePromotionsListOtherSurfaces(t *testing.T) {
	src := &fakePromotionViewerSource{}
	rec := httptest.NewRecorder()
	(&PromotionsHandler{source: src}).HandleNativeList(rec, newPromotionsRequest("/promotions?surface=detail&content_id=movie-1"))
	body := decodeNativePromotions(t, rec)
	if _, ok := body["home_position"]; ok || string(body["promotions"]) != "[]" {
		t.Fatalf("body = %v", body)
	}
	if src.query.Surface != "detail" || src.query.ContentID != "movie-1" {
		t.Fatalf("query = %+v", src.query)
	}
	for _, target := range []string{"/promotions", "/promotions?surface=login"} {
		rec := httptest.NewRecorder()
		(&PromotionsHandler{source: src}).HandleNativeList(rec, newPromotionsRequest(target))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", target, rec.Code)
		}
	}
}

func TestNativePromotionDismiss(t *testing.T) {
	route := func(h *PromotionsHandler, target string) *httptest.ResponseRecorder {
		r := chi.NewRouter()
		r.Put("/promotions/{id}/dismissals/{surface}", h.HandleDismiss)
		rec := httptest.NewRecorder()
		req := newPromotionsRequest(target)
		req.Method = http.MethodPut
		r.ServeHTTP(rec, req)
		return rec
	}
	src := &fakePromotionViewerSource{}
	if rec := route(&PromotionsHandler{source: src}, "/promotions/01PROMO/dismissals/Home"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(src.dismissed) != 1 || src.dismissed[0] != "p1|home|01PROMO" {
		t.Fatalf("dismissed = %v", src.dismissed)
	}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{err: errors.Join(promotions.ErrInvalid, errors.New("surface")), want: http.StatusBadRequest},
		{err: promotions.ErrDismissalUnavailable, want: http.StatusServiceUnavailable},
		{err: errors.New("store down"), want: http.StatusInternalServerError},
	} {
		if rec := route(&PromotionsHandler{source: &fakePromotionViewerSource{dismissErr: tc.err}}, "/promotions/01PROMO/dismissals/home"); rec.Code != tc.want {
			t.Fatalf("%v: status = %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
	// A source without dismissals (or no source) is unavailable, not a panic.
	if rec := route(&PromotionsHandler{source: &fakePromotionSource{}}, "/promotions/01PROMO/dismissals/home"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := route(&PromotionsHandler{}, "/promotions/01PROMO/dismissals/home"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}
