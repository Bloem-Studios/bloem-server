package sections

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/catalog"
	"github.com/Silo-Server/silo-server/internal/promotions"
)

type fakePromoSource struct {
	cards  []promotions.Card
	viewer promotions.Viewer
	err    error
}

func (f *fakePromoSource) ActiveHome(_ context.Context, v promotions.Viewer) ([]promotions.Card, int, error) {
	f.viewer = v
	return f.cards, promotions.DefaultHomePosition, f.err
}

func TestFetchOnePromotedDeliversCardsInSourceOrderAndHonoursItemLimit(t *testing.T) {
	src := &fakePromoSource{cards: []promotions.Card{{ID: "high", Headline: "High"}, {ID: "mid", Headline: "Mid"}, {ID: "low", Headline: "Low"}}}
	f := &Fetcher{Promotions: src}
	resolved := ResolvedSection{ID: "system-promoted", SectionType: SectionPromoted, Title: "Promoted", ItemLimit: 2}

	got, err := f.FetchOne(context.Background(), resolved, nil, nil, 7, "p1", catalog.AccessFilter{AllowedLibraryIDs: []int{3}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 || got.TotalCount != 3 || len(got.Promos) != 2 || got.Promos[0].ID != "high" || got.Promos[1].ID != "mid" {
		t.Fatalf("unexpected section: items=%d total=%d promos=%+v", len(got.Items), got.TotalCount, got.Promos)
	}
	if src.viewer.UserID != 7 || src.viewer.ProfileID != "p1" || len(src.viewer.LibraryIDs) != 1 || src.viewer.LibraryIDs[0] != 3 {
		t.Fatalf("viewer not forwarded: %+v", src.viewer)
	}
}

func TestFetchOnePromotedWithoutSourceOrUserIsEmpty(t *testing.T) {
	resolved := ResolvedSection{ID: "system-promoted", SectionType: SectionPromoted}
	got, err := (&Fetcher{}).FetchOne(context.Background(), resolved, nil, nil, 7, "p1", catalog.AccessFilter{})
	if err != nil || len(got.Promos) != 0 || got.Items == nil {
		t.Fatalf("nil source: %+v %v", got, err)
	}
	got, err = (&Fetcher{Promotions: &fakePromoSource{cards: []promotions.Card{{ID: "x"}}}}).FetchOne(context.Background(), resolved, nil, nil, 0, "", catalog.AccessFilter{})
	if err != nil || len(got.Promos) != 0 {
		t.Fatalf("anonymous: %+v %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := (&Fetcher{Promotions: &fakePromoSource{err: boom}}).FetchOne(context.Background(), resolved, nil, nil, 7, "p1", catalog.AccessFilter{}); !errors.Is(err, boom) {
		t.Fatalf("source error must surface: %v", err)
	}
	if !ValidSectionTypes[SectionPromoted] {
		t.Fatal("promoted must be a valid section type")
	}
}
