package managedtracking

import (
	"context"
	"errors"
	"testing"

	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
)

type recommendationsFixture struct {
	*calendarFixtureSource
	items []*rec.Recommendation
}

func (f *recommendationsFixture) Recommendations(context.Context, Profile) ([]*rec.Recommendation, error) {
	if f.afterRead != nil {
		f.afterRead()
	}
	return f.items, nil
}
func TestRecommendationsOnlyExportCurrentEnrolledProfile(t *testing.T) {
	f := enrolledFixture(t)
	source := &recommendationsFixture{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}, items: []*rec.Recommendation{{ContentId: "movie", Kind: "movie", Title: "Personal AI result", Reason: "Taste match"}}}
	f.s.Calendar = source
	r := &rec.RecommendationsRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID}
	page, err := f.s.ReadRecommendations(t.Context(), f.id, r)
	if err != nil || len(page.Items) != 1 || page.Items[0].Reason != "Taste match" || page.ExpiresAt == nil {
		t.Fatalf("personal result: %v %v", page, err)
	}
	r.ProfileId = "foreign"
	if _, err = f.s.ReadRecommendations(t.Context(), f.id, r); !errors.Is(err, ErrAuthority) {
		t.Fatalf("foreign profile: %v", err)
	}
	r.ProfileId = f.p.ProfileID
	source.afterRead = func() { source.digest = "changed" }
	if _, err = f.s.ReadRecommendations(t.Context(), f.id, r); !errors.Is(err, ErrAuthority) {
		t.Fatalf("changed authority: %v", err)
	}
}
