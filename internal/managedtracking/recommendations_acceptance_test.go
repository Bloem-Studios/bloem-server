package managedtracking

import (
	"context"
	"os"
	"testing"
	"time"

	rec "github.com/Silo-Server/silo-server/internal/managedtracking/recommendationswire"
)

type realRecommendationsFixtureSource struct{ realCalendarFixtureSource }

func (realRecommendationsFixtureSource) Recommendations(_ context.Context, p Profile) ([]*rec.Recommendation, error) {
	return []*rec.Recommendation{{ContentId: "personal-" + p.ProfileID, Kind: "movie", Title: "Personal model result", Reason: "Taste match", ExternalIds: map[string]string{"tmdb": "42"}}}, nil
}
func TestRealPluginSharesAIRecommendations(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.SetCalendarSource(realRecommendationsFixtureSource{})
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var out map[string]int
		status, _ := run.call("GET", "/fixture/ai-recommendations", nil, &out)
		if status != 200 {
			t.Fatalf("fixture response %d", status)
		}
		if out[p.ProfileID] == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("personal AI projection did not converge: %v", out)
		case <-ticker.C:
		}
	}
	run.finish()
}

func TestRealPluginRecommendationFeedbackRoundTrip(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.SetCalendarSource(realRecommendationsFixtureSource{})
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("feedback did not converge")
			case <-ticker.C:
			}
		}
	}
	wait(func() bool {
		var out map[string]int
		status, _ := run.call("GET", "/fixture/ai-recommendations", nil, &out)
		return status == 200 && out[p.ProfileID] == 1
	})
	content := "personal-" + p.ProfileID
	for i, dismissed := range []bool{true, false} {
		status, _ := run.call("POST", "/fixture/recommendation-feedback", map[string]any{"Profile": p.ProfileID, "Content": content, "Dismissed": dismissed}, nil)
		if status != 200 {
			t.Fatalf("choice failed: %d", status)
		}
		run.reconcile()
		wait(func() bool {
			var revision int
			var value bool
			err := s.Pool.QueryRow(ctx, `SELECT revision,dismissed FROM bloem_managed_recommendation_feedback WHERE installation_id=$1 AND user_id=$2 AND profile_id=$3 AND content_id=$4`, id, p.AccountID, p.ProfileID, content).Scan(&revision, &value)
			return err == nil && revision == i+1 && value == dismissed
		})
		feed, err := s.ReadRecommendations(ctx, id, &rec.RecommendationsRequest{AccountId: p.AccountID, ProfileId: p.ProfileID})
		want := 1
		if dismissed {
			want = 0
		}
		if err != nil || len(feed.Items) != want {
			t.Fatal("host feedback projection", feed, err)
		}
	}
	run.finish()
}
