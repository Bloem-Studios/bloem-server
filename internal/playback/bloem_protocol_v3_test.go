package playback

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestDeploymentFeaturesV3AddsReadinessOnlyWhenEnabled(t *testing.T) {
	if slices.Contains(DeploymentFeaturesV3(false), FeatureHeaderAuthenticatedMediaReadyV3) {
		t.Fatal("disabled deployment advertised readiness")
	}
	if !slices.Contains(DeploymentFeaturesV3(true), FeatureHeaderAuthenticatedMediaReadyV3) {
		t.Fatal("enabled deployment omitted readiness")
	}
}

func TestNegotiateClientFeaturesV3HonorsDeploymentReadiness(t *testing.T) {
	tests := []struct {
		name      string
		requested []string
		ready     bool
		want      []string
	}{
		{
			name:      "disabled removes header transport and dependent origins",
			requested: []string{FeaturePlaybackPlanV3, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3},
			want:      []string{FeaturePlaybackPlanV3},
		},
		{
			name:      "software decode remains independent",
			requested: []string{FeaturePlaybackPlanV3, FeatureSoftwareVideoDecodeV3, FeatureHeaderAuthenticatedMediaV3},
			want:      []string{FeaturePlaybackPlanV3, FeatureSoftwareVideoDecodeV3},
		},
		{
			name:      "client transformation declaration remains independent",
			requested: []string{FeaturePlaybackPlanV3, FeatureClientVideoTransforms, FeatureHeaderAuthenticatedMediaV3},
			want:      []string{FeaturePlaybackPlanV3, FeatureClientVideoTransforms},
		},
		{
			name:      "ready deployment accepts header transport and origins",
			requested: []string{FeaturePlaybackPlanV3, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3},
			ready:     true,
			want:      []string{FeaturePlaybackPlanV3, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3},
		},
		{
			name:      "origins cannot be accepted without header transport",
			requested: []string{FeaturePlaybackPlanV3, FeatureAuthorizedMediaOriginsV3},
			ready:     true,
			want:      []string{FeaturePlaybackPlanV3},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NegotiateClientFeaturesV3(test.requested, test.ready); !slices.Equal(got, test.want) {
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
