package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

var (
	promoStart = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	promoEnd   = time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
)

func promoCard(id string) promotions.Card {
	return promotions.Card{
		ID:          id,
		Kicker:      "New this week",
		Headline:    "The Bloem Winter Collection",
		Subtitle:    "Ten films, one long weekend.",
		ImageURL:    "https://cdn.example/winter-16x9.jpg",
		Deeplink:    "bloem://collection/winter",
		CTA:         &promotions.CTA{Label: "Browse", URL: "/collections/winter"},
		Dismissible: true,
	}
}

type fakePromotionSource struct {
	cards []promotions.Card
	query promotions.Query
	err   error
}

func (f *fakePromotionSource) Active(_ context.Context, q promotions.Query) ([]promotions.Card, error) {
	f.query = q
	return f.cards, f.err
}

func newPromotionsRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 7, Role: "user"})
	return req.WithContext(apimw.SetProfileID(ctx, "p1"))
}

func TestPromotionsListForwardsSurfaceContentAndViewer(t *testing.T) {
	src := &fakePromotionSource{cards: []promotions.Card{promoCard("01PROMO")}}
	h := &PromotionsHandler{source: src}
	rec := httptest.NewRecorder()
	h.HandleList(rec, newPromotionsRequest("/promotions?surface=Detail&content_id=movie-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if src.query.Surface != "detail" || src.query.ContentID != "movie-1" || src.query.Viewer.UserID != 7 || src.query.Viewer.ProfileID != "p1" {
		t.Fatalf("query not forwarded: %+v", src.query)
	}
	var resp struct {
		Surface    string            `json:"surface"`
		Promotions []json.RawMessage `json:"promotions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Surface != "detail" || len(resp.Promotions) != 1 {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	assertNoTimerFields(t, resp.Promotions[0])
	t.Logf("GET /promotions body: %s", rec.Body.String())
}

// assertNoTimerFields enforces amendment 3: the server cannot request a
// timer or forced wait, so no such key may ever appear on a card.
func assertNoTimerFields(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"id": true, "kicker": true, "headline": true, "subtitle": true, "image_url": true, "deeplink": true, "cta": true, "dismissible": true, "expires_at": true, "duration_seconds": true, "playback_style": true, "video_url": true}
	for k := range keys {
		if !allowed[k] {
			t.Fatalf("unexpected card key %q", k)
		}
		for _, banned := range []string{"timer", "wait", "delay", "skip", "countdown"} {
			if strings.Contains(strings.ToLower(k), banned) {
				t.Fatalf("card carries a %q field: %s", banned, raw)
			}
		}
	}
}

func TestPromotionsListRejectsHomeAndUnknownSurfaces(t *testing.T) {
	h := &PromotionsHandler{source: &fakePromotionSource{}}
	for _, target := range []string{"/promotions", "/promotions?surface=home", "/promotions?surface=login"} {
		rec := httptest.NewRecorder()
		h.HandleList(rec, newPromotionsRequest(target))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", target, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.HandleList(rec, newPromotionsRequest("/promotions?surface=pre_playback"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"promotions":[]`) {
		t.Fatalf("empty list must be [] not null: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPromotionsListUnavailableAndErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	NewPromotionsHandler(nil).HandleList(rec, newPromotionsRequest("/promotions?surface=detail"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	(&PromotionsHandler{source: &fakePromotionSource{err: errors.New("boom")}}).HandleList(rec, newPromotionsRequest("/promotions?surface=detail"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("source error: %d", rec.Code)
	}
}

func TestHomeDismissalSurfaceAcceptsPromoSurfaces(t *testing.T) {
	for _, s := range []string{"continue_watching", "next_up", "promo:home", "promo:detail", "promo:pre_playback"} {
		if !validHomeSurface(s) {
			t.Fatalf("%s must be valid", s)
		}
	}
	for _, s := range []string{"promo", "promo:", "promo:login", "watchlist"} {
		if validHomeSurface(s) {
			t.Fatalf("%s must be invalid", s)
		}
	}
	// The upsert handler validates the surface before touching the store:
	// an invalid one is 400 with no store provider needed.
	h := NewHomeDismissalHandler(nil)
	router := chi.NewRouter()
	router.Put("/home/dismissals/{surface}/{item_id}", h.HandleUpsertDismissal)
	req := httptest.NewRequest(http.MethodPut, "/home/dismissals/promo:login/01PROMO", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid promo surface: %d", rec.Code)
	}
}
