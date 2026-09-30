package handlers

import (
	"github.com/Silo-Server/silo-server/internal/playback"
	"testing"
)

func TestBloemNativeDecisionPreservesDeploymentReadiness(t *testing.T) {
	for _, ready := range []bool{false, true} {
		response := withNativeServerFeaturesV3(playback.DecisionResponseV3{ServerFeatures: playback.DeploymentFeaturesV3(ready)})
		if got := playback.HasFeatureV3(response.ServerFeatures, playback.FeatureHeaderAuthenticatedMediaReadyV3); got != ready {
			t.Fatalf("native readiness=%v, want %v", got, ready)
		}
		for _, feature := range playback.NativeServerFeaturesV3() {
			if !playback.HasFeatureV3(response.ServerFeatures, feature) {
				t.Fatalf("native response lost %s", feature)
			}
		}
	}
	if got := withNativeServerFeaturesV3(playback.DecisionResponseV3{}); len(got.ServerFeatures) != 0 {
		t.Fatal("empty error response gained features")
	}
}
