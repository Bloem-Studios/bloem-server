package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/previewcontext"
	"github.com/Silo-Server/silo-server/internal/sections"
	"github.com/Silo-Server/silo-server/internal/sections/recipes"
)

func TestPreviewSectionsHidePersonalAndSocial(t *testing.T) {
	rows := []sections.ResolvedSection{{ID: "global", SectionType: sections.SectionRecentlyAdded}, {ID: "continue", SectionType: sections.SectionContinueWatching}, {ID: "favorites", SectionType: sections.SectionFavorites}, {ID: "watchlist", SectionType: sections.SectionWatchlist}, {ID: "next", SectionType: sections.SectionNextUp}}
	for _, recipe := range recipes.Default.List() {
		category := recipe.Definition().Category
		if category == recipes.CategoryPersonalized || category == recipes.CategorySocial {
			rows = append(rows, sections.ResolvedSection{ID: recipe.Type(), SectionType: sections.SectionType(recipe.Type())})
		}
	}
	got := filterBloemPreviewSections(previewcontext.With(t.Context()), rows)
	if len(got) != 1 || got[0].ID != "global" {
		t.Fatalf("personal/social rows escaped: %+v", got)
	}
	if len(filterBloemPreviewSections(t.Context(), rows)) != len(rows) {
		t.Fatal("subscriber layout changed")
	}
}
func TestPreviewHiddenSectionCannotBeReadDirectly(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	h := NewSectionHandler(sections.NewRepository(f.pool), nil)
	ctx := previewcontext.With(t.Context())
	for _, scope := range []string{"home", "library"} {
		var library *int
		if scope == "library" {
			library = &f.library
		}
		row, err := h.repo.Create(t.Context(), &sections.PageSection{Scope: scope, LibraryID: library, SectionType: sections.SectionContinueWatching, Title: "Continue", Enabled: true, ItemLimit: 10})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = f.pool.Exec(t.Context(), `DELETE FROM page_sections WHERE id=$1`, row.ID) })
	}
	for _, scope := range []string{"home", "library"} {
		rows, _, _, _, err := h.loadResolvedHomeSections(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if scope == "library" {
			rows, _, _, err = h.loadResolvedLibrarySections(t.Context(), f.library)
			if err != nil {
				t.Fatal(err)
			}
		}
		found := false
		for _, row := range rows {
			recipe, ok := recipes.Get(string(row.SectionType))
			if !ok || (recipe.Definition().Category != recipes.CategoryPersonalized && row.SectionType != sections.SectionContinueWatching) {
				continue
			}
			found = true
			_, err = h.ResolveCatalogSection(ctx, catalog.CatalogRequest{Scope: scope, LibraryID: f.library, SectionID: row.ID})
			if !errors.Is(err, catalog.ErrCatalogSourceNotFound) {
				t.Fatalf("direct %s/%s accepted: %v", scope, row.ID, err)
			}
		}
		if !found {
			t.Fatal("fixture has no personal row")
		}
	}
}

type previewArtworkSigner struct{ calls []string }

func (s *previewArtworkSigner) ResolveImageURL(_ context.Context, p, v string) string {
	s.calls = append(s.calls, p)
	return "signed:" + p
}
func (s *previewArtworkSigner) ResolveImageURLs(ctx context.Context, ps []string, v string) map[string]string {
	out := map[string]string{}
	for _, p := range ps {
		out[p] = s.ResolveImageURL(ctx, p, v)
	}
	return out
}
func TestPreviewRetainsItemCoverWithoutMintingChapterCover(t *testing.T) {
	f := newPagingIntegrationFixture(t)
	f.exec(t, `UPDATE media_items SET type='manga_chapter',poster_path='local/chapter/poster/original.webp' WHERE content_id=$1`, f.ids[0])
	signer := &previewArtworkSigner{}
	resolver := catalog.BloemPreviewImages(signer, f.pool)
	ctx := previewcontext.With(t.Context())
	got := resolver.ResolveImageURLs(ctx, []string{"chapter-images/7/0/w300.webp", "chapter-images/7/marker-intro/w300.webp", "local/chapter/poster/original.webp", "local/chapter/poster/w300.webp", "local/book/poster/original.webp"}, "card")
	if len(got) != 1 || got["local/book/poster/original.webp"] == "" || len(signer.calls) != 1 {
		t.Fatalf("chapter cover minted or item cover lost: %+v %+v", got, signer.calls)
	}
}
