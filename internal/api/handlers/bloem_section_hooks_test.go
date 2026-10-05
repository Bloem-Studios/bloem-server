package handlers

import (
	"context"
	"net/url"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/sections"
)

// A non-promotion extension traverses the same layout, resolver and projection
// hooks, with the original profile/library facts and without media DB access.
func TestSectionExtensionHooksSupportNonPromotion(t *testing.T) {
	const extension sections.SectionType = "test-banner"
	fetcher := sections.NewFetcher(nil)
	fetcher.SectionResolvers = map[sections.SectionType]sections.SectionResolver{
		extension: func(ctx context.Context, request sections.SectionFetchRequest) (sections.SectionWithItems, error) {
			if request.UserID != 7 || request.ProfileID != "p1" || len(request.Access.AllowedLibraryIDs) != 1 || request.Access.AllowedLibraryIDs[0] != 3 {
				t.Fatalf("host authority changed: %+v", request)
			}
			return sections.SectionWithItems{ResolvedSection: request.Section, Items: []*models.MediaItem{}, TotalCount: 1}, nil
		},
	}
	handler := NewSectionHandler(nil, fetcher)
	handler.HomeTransformers = []HomeSectionTransformer{func(_ context.Context, rows []sections.ResolvedSection, options url.Values) []sections.ResolvedSection {
		if options.Get("banner") == "1" {
			rows = append(rows, sections.ResolvedSection{ID: "banner-row", SectionType: extension, ExtensionData: "Host banner"})
		}
		return rows
	}}
	handler.SectionItemExtensions = []SectionItemExtension{func(section sections.SectionWithItems) []SectionItemView {
		title, ok := section.ExtensionData.(string)
		if !ok {
			return nil
		}
		return []SectionItemView{{ContentID: "banner-card", Type: "banner", Title: title, Genres: []string{}, Keywords: []string{}}}
	}}
	ctx := context.Background()
	if rows := handler.transformHomeSections(ctx, nil, nil); len(rows) != 0 {
		t.Fatalf("extension ignored opt-in: %+v", rows)
	}
	rows := handler.transformHomeSections(ctx, nil, url.Values{"banner": []string{"1"}})
	got, err := fetcher.FetchOne(ctx, rows[0], nil, []int{3}, 7, "p1", catalog.AccessFilter{AllowedLibraryIDs: []int{3}})
	if err != nil {
		t.Fatal(err)
	}
	view := handler.buildSections(ctx, []sections.SectionWithItems{got}, nil, catalog.AccessFilter{}, "")
	if len(view.Sections) != 1 || len(view.Sections[0].Items) != 1 || view.Sections[0].Items[0].Title != "Host banner" || view.Sections[0].Items[0].Promo != nil {
		t.Fatalf("generic projection: %+v", view)
	}
}
