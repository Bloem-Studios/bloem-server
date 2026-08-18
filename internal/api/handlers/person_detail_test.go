package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
)

// --- fakes -------------------------------------------------------------

type fakePersonSource struct {
	byID map[int64]*models.Person
}

func (f *fakePersonSource) Get(_ context.Context, id int64) (*models.Person, error) {
	p, ok := f.byID[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return p, nil
}

type fakePersonBrowse struct {
	result *catalog.BrowseResult
	err    error

	lastFilters catalog.BrowseFilters
}

func (f *fakePersonBrowse) BrowsePage(_ context.Context, filters catalog.BrowseFilters, _ bool) (*catalog.BrowseResult, error) {
	f.lastFilters = filters
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

type fakePersonRoles struct {
	byContentID map[string]catalog.PersonItemRole
	err         error
}

func (f *fakePersonRoles) RolesForItems(_ context.Context, _ int64, contentIDs []string) (map[string]catalog.PersonItemRole, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]catalog.PersonItemRole, len(contentIDs))
	for _, id := range contentIDs {
		if role, ok := f.byContentID[id]; ok {
			out[id] = role
		}
	}
	return out, nil
}

// --- request helper ------------------------------------------------------

func personDetailRequest(personID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v2/persons/"+personID, nil)
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("person_id", personID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

// --- tests -----------------------------------------------------------------

func TestHandleGetPersonDetail_ComposesBioAndFilmography(t *testing.T) {
	birth := time.Date(1974, 11, 11, 0, 0, 0, 0, time.UTC)
	persons := &fakePersonSource{byID: map[int64]*models.Person{
		42: {
			ID:         42,
			Name:       "Riva Okonkwo",
			Bio:        "An invented performer.",
			BirthDate:  &birth,
			Birthplace: "Lagos",
			PhotoPath:  "people/42/original.jpg",
		},
	}}
	browse := &fakePersonBrowse{result: &catalog.BrowseResult{Items: []*models.MediaItem{
		{ContentID: "movie-1", Type: itemTypeMovie, Title: "The Invented Crossing", Year: 2019, PosterPath: "posters/movie-1/original.jpg"},
		{ContentID: "series-1", Type: itemTypeSeries, Title: "Eight Quiet Rooms", Year: 2016},
	}}}
	roles := &fakePersonRoles{byContentID: map[string]catalog.PersonItemRole{
		"movie-1":  {Kind: models.PersonKindActor, Character: "Ada"},
		"series-1": {Kind: models.PersonKindDirector},
	}}
	images := fakeImageResolver{byPath: map[string]string{
		"people/42/original.jpg":       "https://cdn.example.test/people/42/featured.jpg",
		"posters/movie-1/original.jpg": "https://cdn.example.test/posters/movie-1/card.jpg",
	}}

	h := NewPersonDetailHandler(persons, browse, roles, images)

	rec := httptest.NewRecorder()
	h.HandleGetPersonDetail(rec, personDetailRequest("42"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	var body personDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}

	if body.ID != "42" {
		t.Errorf("id = %q, want %q", body.ID, "42")
	}
	if body.Name != "Riva Okonkwo" {
		t.Errorf("name = %q", body.Name)
	}
	if body.Bio != "An invented performer." {
		t.Errorf("bio = %q", body.Bio)
	}
	if body.BirthDate == nil || *body.BirthDate != "1974-11-11" {
		t.Errorf("birth_date = %v, want 1974-11-11", body.BirthDate)
	}
	if body.DeathDate != nil {
		t.Errorf("death_date = %v, want nil", body.DeathDate)
	}
	if body.Birthplace != "Lagos" {
		t.Errorf("birthplace = %q", body.Birthplace)
	}
	if body.PhotoURL != "https://cdn.example.test/people/42/featured.jpg" {
		t.Errorf("photo_url = %q", body.PhotoURL)
	}

	if len(body.Filmography) != 2 {
		t.Fatalf("filmography len = %d, want 2; body: %+v", len(body.Filmography), body.Filmography)
	}
	movie := body.Filmography[0]
	if movie.ContentID != "movie-1" || movie.Kind != itemTypeMovie || movie.Year != 2019 {
		t.Errorf("filmography[0] = %+v", movie)
	}
	if movie.Role != "Ada" {
		t.Errorf("filmography[0].role = %q, want Ada", movie.Role)
	}
	if movie.PosterURL != "https://cdn.example.test/posters/movie-1/card.jpg" {
		t.Errorf("filmography[0].poster_url = %q", movie.PosterURL)
	}
	series := body.Filmography[1]
	if series.ContentID != "series-1" || series.Kind != itemTypeSeries {
		t.Errorf("filmography[1] = %+v", series)
	}
	if series.Role != "Director" {
		t.Errorf("filmography[1].role = %q, want Director", series.Role)
	}
	if series.PosterURL != "" {
		t.Errorf("filmography[1].poster_url = %q, want empty (no poster on this item)", series.PosterURL)
	}

	// The browse call must have been scoped to this person, movies and series
	// only, and sorted by year descending — the filter this whole visibility
	// story depends on.
	if browse.lastFilters.PersonID != 42 {
		t.Errorf("browse PersonID = %d, want 42", browse.lastFilters.PersonID)
	}
	if browse.lastFilters.Sort != "year" || browse.lastFilters.Order != "desc" {
		t.Errorf("browse sort = %q/%q, want year/desc", browse.lastFilters.Sort, browse.lastFilters.Order)
	}
}

func TestHandleGetPersonDetail_UnknownPersonReturns404(t *testing.T) {
	h := NewPersonDetailHandler(&fakePersonSource{byID: map[int64]*models.Person{}}, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.HandleGetPersonDetail(rec, personDetailRequest("999"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetPersonDetail_InvalidIDReturns400(t *testing.T) {
	h := NewPersonDetailHandler(&fakePersonSource{byID: map[int64]*models.Person{}}, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.HandleGetPersonDetail(rec, personDetailRequest("not-a-number"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetPersonDetail_NoBrowseSourceReturnsEmptyFilmography(t *testing.T) {
	persons := &fakePersonSource{byID: map[int64]*models.Person{
		7: {ID: 7, Name: "Solo Person"},
	}}
	h := NewPersonDetailHandler(persons, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.HandleGetPersonDetail(rec, personDetailRequest("7"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body personDetailResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Filmography) != 0 {
		t.Errorf("filmography = %+v, want empty", body.Filmography)
	}
}

func TestHandleGetPersonDetail_NoPersonSourceReturns503(t *testing.T) {
	h := NewPersonDetailHandler(nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	h.HandleGetPersonDetail(rec, personDetailRequest("1"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", rec.Code, rec.Body.String())
	}
}
