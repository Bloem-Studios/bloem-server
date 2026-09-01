package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/database"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
	"github.com/Silo-Server/silo-server/migrations"
)

// promotedHomeWire is the subset of the three home responses the opt-in
// contract is about: section types and item types.
type promotedHomeWire struct {
	Sections []promotedHomeWireSection `json:"sections"`
	Section  *promotedHomeWireSection  `json:"section"`
}

type promotedHomeWireSection struct {
	ID          string `json:"id"`
	SectionType string `json:"section_type"`
	Items       []struct {
		ContentID string          `json:"content_id"`
		Type      string          `json:"type"`
		Promo     json.RawMessage `json:"promo"`
	} `json:"items"`
}

func (w promotedHomeWire) rows() []promotedHomeWireSection {
	if w.Section != nil {
		return []promotedHomeWireSection{*w.Section}
	}
	return w.Sections
}

// TestHomeEndpointsDeliverPromotedOnlyWhenOptedIn drives the real
// /home/layout, /home/sections and /home/sections/{id}/items handlers over a
// migrated database holding one active home campaign: without `promoted=1`
// no response carries a `promoted` row or a `promo` item (the items route
// 404s for the synthetic id); with it the row and its card appear.
func TestHomeEndpointsDeliverPromotedOnlyWhenOptedIn(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool := newCompatAdapterDisposableDatabase(t, ctx, dsn)
	if err := database.RunMigrations(ctx, pool, migrations.FS, "sql"); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	var userID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ('promo-viewer', 'promo-viewer@example.test', 'x', 'user', true) RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	svc := promotions.NewService(pool, recipes.FixedClock(promoStart.Add(time.Hour)), nil)
	created, err := svc.Create(ctx, userID, promotions.Input{
		Surfaces: []string{"home"},
		Kicker:   "New this week",
		Headline: "The Bloem Winter Collection",
		ImageURL: "https://cdn.example/winter-16x9.jpg",
		Deeplink: "bloem://collection/winter",
		StartsAt: promoStart,
		EndsAt:   promoEnd,
	})
	if err != nil {
		t.Fatalf("create promotion: %v", err)
	}

	fetcher := sections.NewFetcher(pool)
	fetcher.Promotions = svc
	h := NewSectionHandler(sections.NewRepository(pool), fetcher)
	h.Promotions = svc

	call := func(t *testing.T, target, sectionID string, handler http.HandlerFunc) (int, promotedHomeWire, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		reqCtx := apimw.SetClaims(req.Context(), &auth.Claims{UserID: userID, Role: "user"})
		reqCtx = apimw.SetProfileID(reqCtx, "profile-1")
		if sectionID != "" {
			routeCtx := chi.NewRouteContext()
			routeCtx.URLParams.Add("id", sectionID)
			reqCtx = context.WithValue(reqCtx, chi.RouteCtxKey, routeCtx)
		}
		rec := httptest.NewRecorder()
		handler(rec, req.WithContext(reqCtx))
		var wire promotedHomeWire
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
				t.Fatalf("%s: decode: %v\n%s", target, err, rec.Body.String())
			}
		}
		return rec.Code, wire, rec.Body.Bytes()
	}
	promotedRows := func(wire promotedHomeWire) (promotedSections, promoItems int) {
		for _, s := range wire.rows() {
			if s.SectionType == "promoted" || s.ID == SystemPromotedSectionID {
				promotedSections++
			}
			for _, it := range s.Items {
				if it.Type == "promo" || it.Promo != nil {
					promoItems++
				}
			}
		}
		return promotedSections, promoItems
	}

	t.Run("without opt-in", func(t *testing.T) {
		for _, tc := range []struct {
			target  string
			handler http.HandlerFunc
		}{
			{"/home/layout", h.HandleHomeLayout},
			{"/home/sections", h.HandleHomeSections},
			{"/home/sections?promoted=0", h.HandleHomeSections},
			{"/home/layout?promoted=yes", h.HandleHomeLayout},
		} {
			code, wire, body := call(t, tc.target, "", tc.handler)
			if code != http.StatusOK || len(wire.rows()) == 0 {
				t.Fatalf("%s: status %d, rows %d: %s", tc.target, code, len(wire.rows()), body)
			}
			if secs, items := promotedRows(wire); secs != 0 || items != 0 {
				t.Fatalf("%s: promoted delivered without opt-in (sections=%d items=%d): %s", tc.target, secs, items, body)
			}
		}
		code, _, body := call(t, "/home/sections/"+SystemPromotedSectionID+"/items", SystemPromotedSectionID, h.HandleHomeSectionItems)
		if code != http.StatusNotFound {
			t.Fatalf("items without opt-in: want 404, got %d: %s", code, body)
		}
	})

	t.Run("with opt-in", func(t *testing.T) {
		for _, tc := range []struct {
			target  string
			handler http.HandlerFunc
		}{
			{"/home/layout?promoted=1", h.HandleHomeLayout},
			{"/home/sections?promoted=1", h.HandleHomeSections},
		} {
			code, wire, body := call(t, tc.target, "", tc.handler)
			if code != http.StatusOK {
				t.Fatalf("%s: status %d: %s", tc.target, code, body)
			}
			if secs, _ := promotedRows(wire); secs != 1 {
				t.Fatalf("%s: want one promoted row: %s", tc.target, body)
			}
		}
		code, wire, body := call(t, "/home/sections/"+SystemPromotedSectionID+"/items?promoted=1", SystemPromotedSectionID, h.HandleHomeSectionItems)
		if code != http.StatusOK || wire.Section == nil || wire.Section.SectionType != "promoted" || len(wire.Section.Items) != 1 {
			t.Fatalf("items with opt-in: status %d: %s", code, body)
		}
		item := wire.Section.Items[0]
		if item.Type != "promo" || item.ContentID != created.ID || item.Promo == nil {
			t.Fatalf("items with opt-in: unexpected item: %s", body)
		}
		t.Logf("promoted items wire: %s", body)
	})
}
