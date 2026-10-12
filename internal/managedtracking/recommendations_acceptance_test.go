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
