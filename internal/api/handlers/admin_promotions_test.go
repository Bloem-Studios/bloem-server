package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

type fakePromotionRegistry struct {
	rows      []promotions.Promotion
	created   []promotions.Input
	createdBy int
	updated   map[string]promotions.Input
	deleted   []string
	err       error
}

func (f *fakePromotionRegistry) row(id string, in promotions.Input) *promotions.Promotion {
	n, err := promotions.Normalize(in)
	if err != nil {
		return nil
	}
	return &promotions.Promotion{
		ID: id, OrganizationID: n.OrganizationID, Surfaces: n.Surfaces, Placement: n.Placement, Kicker: n.Kicker, Headline: n.Headline,
		Subtitle: n.Subtitle, ImageURL: n.ImageURL, ImageWidth: n.ImageWidth, ImageHeight: n.ImageHeight, Deeplink: n.Deeplink, CTA: n.CTA,
		Priority: n.Priority, StartsAt: n.StartsAt, EndsAt: n.EndsAt, Targeting: n.Targeting, Dismissible: n.Dismissible,
		CreatedBy: f.createdBy, CreatedAt: promoStart, UpdatedAt: promoStart,
	}
}

func (f *fakePromotionRegistry) List(context.Context) ([]promotions.Promotion, error) {
	return f.rows, f.err
}
func (f *fakePromotionRegistry) Create(_ context.Context, createdBy int, in promotions.Input) (*promotions.Promotion, error) {
	f.createdBy = createdBy
	f.created = append(f.created, in)
	if f.err != nil {
		return nil, f.err
	}
	if _, err := promotions.Normalize(in); err != nil {
		return nil, err
	}
	return f.row("01PROMO", in), nil
}
func (f *fakePromotionRegistry) Update(_ context.Context, id string, in promotions.Input) (*promotions.Promotion, error) {
	if f.updated == nil {
		f.updated = map[string]promotions.Input{}
	}
	f.updated[id] = in
	if id == "missing" {
		return nil, promotions.ErrNotFound
	}
	if _, err := promotions.Normalize(in); err != nil {
		return nil, err
	}
	return f.row(id, in), nil
}
func (f *fakePromotionRegistry) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	if id == "missing" {
		return promotions.ErrNotFound
	}
	return f.err
}

func newAdminPromotionsRouter(reg promotionRegistry) http.Handler {
	h := &AdminPromotionsHandler{registry: reg}
	r := chi.NewRouter()
	r.Route("/admin/promotions", func(r chi.Router) {
		r.Get("/", h.HandleList)
		r.Post("/", h.HandleCreate)
		r.Put("/{id}", h.HandleUpdate)
		r.Delete("/{id}", h.HandleDelete)
	})
	return r
}

func adminPromotionRequest(method, target, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return req.WithContext(apimw.SetClaims(req.Context(), &auth.Claims{UserID: 42, Role: "admin"}))
}

const adminPromotionCreateBody = `{
  "surfaces": ["home", "detail", "pre_playback"],
  "placement": {"home_position": 1, "detail_slot": "below_hero", "content_ids": ["movie-1"]},
  "kicker": "New this week",
  "headline": "The Bloem Winter Collection",
  "subtitle": "Ten films, one long weekend.",
  "image_url": "https://cdn.example/winter-16x9.jpg",
  "image_width": 1920,
  "image_height": 1080,
  "deeplink": "bloem://collection/winter",
  "cta": {"label": "Browse", "url": "/collections/winter"},
  "priority": 5,
  "starts_at": "2026-11-20T00:00:00Z",
  "ends_at": "2026-12-01T00:00:00Z",
  "targeting": {"audience": "all"},
  "dismissible": true
}`

func TestAdminPromotionsCreateReturnsCreatedPromotion(t *testing.T) {
	reg := &fakePromotionRegistry{}
	rec := httptest.NewRecorder()
	newAdminPromotionsRouter(reg).ServeHTTP(rec, adminPromotionRequest(http.MethodPost, "/admin/promotions", adminPromotionCreateBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if reg.createdBy != 42 || len(reg.created) != 1 {
		t.Fatalf("create not forwarded with the admin id: %+v", reg)
	}
	var got promotions.Promotion
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "01PROMO" || got.Headline != "The Bloem Winter Collection" || got.CTA == nil || *got.ImageWidth != 1920 || *got.Placement.HomePosition != 1 || !got.Dismissible || got.Priority != 5 || len(got.Surfaces) != 3 || got.Targeting.Audience != "all" {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	t.Logf("admin create response: %s", rec.Body.String())
}

func TestAdminPromotionsRejectsValidationFailuresAndBadJSON(t *testing.T) {
	reg := &fakePromotionRegistry{}
	router := newAdminPromotionsRouter(reg)
	for name, body := range map[string]string{
		"poster aspect": strings.Replace(adminPromotionCreateBody, `"image_height": 1080`, `"image_height": 2880`, 1),
		"http image":    strings.Replace(adminPromotionCreateBody, "https://cdn.example", "http://cdn.example", 1),
		"bad deeplink":  strings.Replace(adminPromotionCreateBody, "bloem://collection/winter", "javascript:alert(1)", 1),
		"no surfaces":   strings.Replace(adminPromotionCreateBody, `["home", "detail", "pre_playback"]`, `[]`, 1),
		"window order":  strings.Replace(adminPromotionCreateBody, "2026-12-01", "2026-11-01", 1),
		"bad json":      "{",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, adminPromotionRequest(http.MethodPost, "/admin/promotions", body))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d: %s", name, rec.Code, rec.Body.String())
		}
	}
	// A timer / forced-wait field is simply not part of the contract: it is
	// ignored on input and never stored or echoed.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodPost, "/admin/promotions", strings.Replace(adminPromotionCreateBody, `"priority": 5,`, `"priority": 5, "wait_seconds": 5, "skip_after": 3,`, 1)))
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "wait") || strings.Contains(rec.Body.String(), "skip") {
		t.Fatalf("timer fields must not exist: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminPromotionsUpdateDeleteAndList(t *testing.T) {
	reg := &fakePromotionRegistry{}
	router := newAdminPromotionsRouter(reg)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodPut, "/admin/promotions/01PROMO", strings.Replace(adminPromotionCreateBody, "Winter", "Spring", 1)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Spring") || reg.updated["01PROMO"].Headline == "" {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodPut, "/admin/promotions/missing", adminPromotionCreateBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodDelete, "/admin/promotions/01PROMO", ""))
	if rec.Code != http.StatusNoContent || len(reg.deleted) != 1 {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodDelete, "/admin/promotions/missing", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: %d", rec.Code)
	}

	reg.rows = nil
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodGet, "/admin/promotions", ""))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"promotions":[]`) || !strings.Contains(rec.Body.String(), `"surfaces":["home","detail","pre_playback"]`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	reg.err = errors.New("boom")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, adminPromotionRequest(http.MethodGet, "/admin/promotions", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list error: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	newAdminPromotionsRouter(nil).ServeHTTP(rec, adminPromotionRequest(http.MethodGet, "/admin/promotions", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired: %d", rec.Code)
	}
}
