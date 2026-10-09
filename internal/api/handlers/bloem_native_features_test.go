package handlers

import (
	"slices"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestBloemNativeDecisionAdvertisesNativeFeatures(t *testing.T) {
	response := withNativeServerFeaturesV3(playback.DecisionResponseV3{ServerFeatures: playback.ServerFeaturesV3()})
	if !slices.Equal(response.ServerFeatures, playback.NativeServerFeaturesV3()) {
		t.Fatalf("native features = %v, want %v", response.ServerFeatures, playback.NativeServerFeaturesV3())
	}
	if got := withNativeServerFeaturesV3(playback.DecisionResponseV3{}); len(got.ServerFeatures) != 0 {
		t.Fatal("empty error response gained features")
	}
}
