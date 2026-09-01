package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/sections"
)

type fakeHomePromoSource struct {
	cards    []promotions.Card
	position int
	calls    int
}

func (f *fakeHomePromoSource) ActiveHome(context.Context, promotions.Viewer) ([]promotions.Card, int, error) {
	f.calls++
	return f.cards, f.position, nil
}

func profileRequest() *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/home/layout", nil)
	ctx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: 7, Role: "user"})
	return req.WithContext(apimw.SetProfileID(ctx, "p1"))
}

func homeRows(types ...sections.SectionType) []sections.ResolvedSection {
	out := make([]sections.ResolvedSection, 0, len(types))
	for i, t := range types {
		out = append(out, sections.ResolvedSection{ID: string(t), SectionType: t, Position: i})
	}
	return out
}

func TestMaybeInjectPromotedInsertsAtPlacementPosition(t *testing.T) {
	src := &fakeHomePromoSource{cards: []promotions.Card{promoCard("a"), promoCard("b")}, position: 2}
	h := &SectionHandler{Promotions: src}
	got := h.maybeInjectPromoted(profileRequest(), homeRows(sections.SectionContinueWatching, sections.SectionRecentlyAdded, sections.SectionWatchlist))
	if len(got) != 4 || got[2].SectionType != sections.SectionPromoted || got[2].ID != SystemPromotedSectionID || got[2].ItemLimit != 2 {
		t.Fatalf("unexpected layout: %+v", got)
	}
	// Positions beyond the layout clamp to the end; the default lands after the first row.
	src.position = 99
	if got := h.maybeInjectPromoted(profileRequest(), homeRows(sections.SectionContinueWatching)); len(got) != 2 || got[1].SectionType != sections.SectionPromoted {
		t.Fatalf("clamp: %+v", got)
	}
	src.position = promotions.DefaultHomePosition
	if got := h.maybeInjectPromoted(profileRequest(), homeRows(sections.SectionContinueWatching, sections.SectionRecentlyAdded)); got[1].SectionType != sections.SectionPromoted {
		t.Fatalf("default: %+v", got)
	}
}

func TestMaybeInjectPromotedSkipsWhenDormantEmptyAnonymousOrAlreadyPresent(t *testing.T) {
	rows := homeRows(sections.SectionContinueWatching, sections.SectionRecentlyAdded)
	if got := (&SectionHandler{}).maybeInjectPromoted(profileRequest(), rows); len(got) != 2 {
		t.Fatalf("dormant: %+v", got)
	}
	empty := &fakeHomePromoSource{}
	if got := (&SectionHandler{Promotions: empty}).maybeInjectPromoted(profileRequest(), rows); len(got) != 2 {
		t.Fatalf("no cards: %+v", got)
	}
	src := &fakeHomePromoSource{cards: []promotions.Card{promoCard("a")}, position: 1}
	anon := httptest.NewRequest(http.MethodGet, "/home/layout", nil)
	if got := (&SectionHandler{Promotions: src}).maybeInjectPromoted(anon, rows); len(got) != 2 || src.calls != 0 {
		t.Fatalf("anonymous must not query: %+v calls=%d", got, src.calls)
	}
	// An admin-configured promoted section keeps its own position.
	existing := homeRows(sections.SectionPromoted, sections.SectionContinueWatching)
	if got := (&SectionHandler{Promotions: src}).maybeInjectPromoted(profileRequest(), existing); len(got) != 2 || got[0].ID != "promoted" || src.calls != 0 {
		t.Fatalf("existing: %+v calls=%d", got, src.calls)
	}
}

// The promoted section's items are the `promo` variant of the section item
// union: type "promo", content_id = promotion id, the card under `promo`.
func TestBuildSectionsResponseEmitsPromoItemVariant(t *testing.T) {
	h := &SectionHandler{}
	withItems := []sections.SectionWithItems{
		{
			ResolvedSection: sections.ResolvedSection{ID: SystemPromotedSectionID, SectionType: sections.SectionPromoted, Title: "Promoted", ItemLimit: 1},
			Items:           []*models.MediaItem{},
			TotalCount:      1,
			Promos:          []promotions.Card{promoCard("01PROMO")},
		},
		{
			ResolvedSection: sections.ResolvedSection{ID: "recent", SectionType: sections.SectionRecentlyAdded, Title: "Recently Added"},
			Items:           []*models.MediaItem{{ContentID: "movie-1", Type: "movie", Title: "A Film", Status: "matched"}},
		},
	}
	resp := h.buildSectionsResponse(httptest.NewRequest(http.MethodGet, "/home/sections", nil), withItems, nil)
	if len(resp.Sections) != 2 || len(resp.Sections[0].Items) != 1 || resp.Sections[0].Items[0].Type != "promo" || resp.Sections[0].Items[0].Promo == nil || resp.Sections[1].Items[0].Promo != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}
	raw, err := json.Marshal(resp.Sections[0])
	if err != nil {
		t.Fatal(err)
	}
	var section struct {
		SectionType string `json:"section_type"`
		Items       []struct {
			ContentID string          `json:"content_id"`
			Type      string          `json:"type"`
			Title     string          `json:"title"`
			Promo     json.RawMessage `json:"promo"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &section); err != nil {
		t.Fatal(err)
	}
	if section.SectionType != "promoted" || section.Items[0].ContentID != "01PROMO" || section.Items[0].Title != "The Bloem Winter Collection" {
		t.Fatalf("unexpected wire: %s", raw)
	}
	assertNoTimerFields(t, section.Items[0].Promo)
	t.Logf("promoted section wire: %s", raw)
}

// Forward compatibility: a pre-S-2 client decodes the layout with its known
// section-type set and drops what it does not know. There is no shared v1
// fixture decoder in this repository, so the old-client behaviour is modelled
// here: the `promoted` row is ignored, the rest of the layout is intact.
func TestOldClientsIgnoreThePromotedSectionType(t *testing.T) {
	layout := homeLayoutResponse{Sections: []resolvedSectionLayoutResponse{
		{ID: "cw", SectionType: "continue_watching", Title: "Continue Watching"},
		{ID: SystemPromotedSectionID, SectionType: "promoted", Title: "Promoted", ItemLimit: 2},
		{ID: "recent", SectionType: "recently_added", Title: "Recently Added"},
	}}
	raw, err := json.Marshal(layout)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Sections []struct {
			ID          string `json:"id"`
			SectionType string `json:"section_type"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"continue_watching": true, "recently_added": true}
	kept := 0
	for _, s := range decoded.Sections {
		if known[s.SectionType] {
			kept++
		}
	}
	if len(decoded.Sections) != 3 || kept != 2 {
		t.Fatalf("old client must keep 2 of 3 rows: %s", raw)
	}
}
