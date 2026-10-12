package managedtracking

import (
	"errors"
	"testing"

	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
)

func TestRecommendationFeedbackOrdersReplayAndUndo(t *testing.T) {
	f := enrolledFixture(t)
	source := &recommendationsFixture{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}, items: []*rec.Recommendation{{ContentId: "movie", Kind: "movie", Title: "Personal AI result"}}}
	f.s.Calendar = source
	in := &rec.RecommendationFeedback{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, ContentId: "movie", Revision: 1, Dismissed: true}
	if _, err := f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); err != nil {
		t.Fatal("replay", err)
	}
	read := &rec.RecommendationsRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID}
	out, err := f.s.ReadRecommendations(t.Context(), f.id, read)
	if err != nil || len(out.Items) != 0 {
		t.Fatal("host retained dismissal", out, err)
	}
	in.Revision = 2
	in.Dismissed = false
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); err != nil {
		t.Fatal(err)
	}
	in.Revision = 1
	in.Dismissed = true
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); err != nil {
		t.Fatal("stale retry", err)
	}
	out, err = f.s.ReadRecommendations(t.Context(), f.id, read)
	if err != nil || len(out.Items) != 1 {
		t.Fatal("stale retry erased undo", out, err)
	}
	in.Revision = 2
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); !errors.Is(err, ErrConflict) {
		t.Fatal("changed revision", err)
	}
	in.ProfileId = "foreign"
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); !errors.Is(err, ErrAuthority) {
		t.Fatal("foreign profile", err)
	}
	in.ProfileId = f.p.ProfileID
	in.ContentId = "not-recommended"
	in.Revision = 3
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); !errors.Is(err, ErrAuthority) {
		t.Fatal("arbitrary content", err)
	}
	in.ContentId = "movie"
	if _, err = f.s.Pool.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); !errors.Is(err, ErrAuthority) {
		t.Fatal("revoked grant", err)
	}
}

func TestRecommendationFeedbackRevocationDuringSourceCallback(t *testing.T) {
	f := enrolledFixture(t)
	source := &recommendationsFixture{calendarFixtureSource: &calendarFixtureSource{digest: "visible"}, items: []*rec.Recommendation{{ContentId: "movie", Kind: "movie", Title: "Personal AI result"}}}
	source.afterRead = func() {
		if _, err := f.s.Pool.Exec(t.Context(), `UPDATE bloem_managed_grants SET active=false WHERE installation_id=$1`, f.id); err != nil {
			t.Fatal(err)
		}
	}
	f.s.Calendar = source
	in := &rec.RecommendationFeedback{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, ContentId: "movie", Revision: 1, Dismissed: true}
	if _, err := f.s.ApplyRecommendationFeedback(t.Context(), f.id, in); !errors.Is(err, ErrAuthority) {
		t.Fatal("revoked callback published", err)
	}
	var count int
	if err := f.s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM bloem_managed_recommendation_feedback WHERE installation_id=$1`, f.id).Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized write persisted", count, err)
	}
}
