package api

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/recommendations"
)

func TestManagedAIRecommendationRowsExcludeCommunityAndColdStart(t *testing.T) {
	rows := []recommendations.ForYouRow{
		{Type: "popular", Items: []recommendations.ScoredItem{{MediaItemID: "popular"}}},
		{Type: "similar_users_liked", Items: []recommendations.ScoredItem{{MediaItemID: "social"}}},
		{Type: "cluster", Label: "Because you enjoy mysteries", Items: []recommendations.ScoredItem{{MediaItemID: "personal", Reason: "Taste match"}, {MediaItemID: "personal"}}},
	}
	got := managedAIItems(rows)
	if len(got) != 1 || got[0].MediaItemID != "personal" || got[0].Reason != "Taste match" {
		t.Fatalf("AI-only rows: %#v", got)
	}
}
