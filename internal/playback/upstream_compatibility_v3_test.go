package playback

import (
	"slices"
	"testing"
)

func TestLatestUpstreamPlaybackVocabularyIsAdditive(t *testing.T) {
	if FeaturePlanInvalidatedV3 != "plan_invalidated_v1" {
		t.Fatalf("FeaturePlanInvalidatedV3 = %q", FeaturePlanInvalidatedV3)
	}
	if ClaimClientManagedDynamicRangeV3 != "client_managed_dynamic_range_v1" {
		t.Fatalf("ClaimClientManagedDynamicRangeV3 = %q", ClaimClientManagedDynamicRangeV3)
	}
	if ClaimClientSelectedAudioTrackV3 != "client_selected_audio_track_v1" {
		t.Fatalf("ClaimClientSelectedAudioTrackV3 = %q", ClaimClientSelectedAudioTrackV3)
	}
	if !slices.Contains(ServerFeaturesV3(), FeaturePlanInvalidatedV3) {
		t.Fatal("server capabilities omit plan invalidation")
	}
}
