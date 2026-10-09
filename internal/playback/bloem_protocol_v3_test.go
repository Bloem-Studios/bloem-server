package playback

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestServerFeaturesV3OmitRemovedReadinessToken(t *testing.T) {
	if slices.Contains(ServerFeaturesV3(), "header_authenticated_media_ready_v1") {
		t.Fatal("server advertised the removed deployment readiness token")
	}
}

func TestNegotiateClientFeaturesV3(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		want      []string
	}{
		{
			name:      "header transport and origins are accepted without deployment setup",
			requested: []string{FeaturePlaybackPlanV3, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3},
			want:      []string{FeaturePlaybackPlanV3, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3},
		},
		{
			name:      "software decode and transformations remain independent",
			requested: []string{FeaturePlaybackPlanV3, FeatureSoftwareVideoDecodeV3, FeatureClientVideoTransforms, FeatureHeaderAuthenticatedMediaV3},
			want:      []string{FeaturePlaybackPlanV3, FeatureSoftwareVideoDecodeV3, FeatureClientVideoTransforms, FeatureHeaderAuthenticatedMediaV3},
		},
		{
			name:      "origins cannot be accepted without header transport",
			requested: []string{FeaturePlaybackPlanV3, FeatureAuthorizedMediaOriginsV3},
			want:      []string{FeaturePlaybackPlanV3},
		},
		{
			name:      "tokens are canonicalized, deduplicated and unknown ones dropped",
			requested: []string{" PLAYBACK_PLAN_V3 ", FeaturePlaybackPlanV3, "not_a_feature"},
			want:      []string{FeaturePlaybackPlanV3},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NegotiateClientFeaturesV3(test.requested); !slices.Equal(got, test.want) {
				t.Fatalf("negotiated = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDecisionResponseV3PublishesNegotiatedFeatures(t *testing.T) {
	response := DecisionResponseV3{NegotiatedClientFeatures: []string{FeaturePlaybackPlanV3}}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"negotiated_client_features":["playback_plan_v3"]`) {
		t.Fatalf("decision JSON = %s", raw)
	}
}
