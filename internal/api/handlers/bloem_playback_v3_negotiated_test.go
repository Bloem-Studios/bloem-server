package handlers

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/nodeauth/nodeauthtest"
	"github.com/Silo-Server/silo-server/internal/playback"
)

// TestBloemReplanReportsDurableNegotiatedFeatures: a replan that tries to drop
// header authentication still reports the attempt's durable client features in
// Bloem's negotiated_client_features, not the downgraded request.
func TestBloemReplanReportsDurableNegotiatedFeatures(t *testing.T) {
	file := v3HandlerFixtureFile(t)
	manager := playback.NewSessionManager(0, 0)
	handler := newKeyedPlaybackHandler(manager, testPlaybackFileResolver{file: file})
	handler.JWTSecret = "test-stream-signing-secret"
	handler.NodeKeys = nodeauthtest.SameKey("test-stream-signing-secret")
	handler.SettingsRepo = &mutablePlaybackSettingsV3{values: map[string]string{"allow_4k_transcode": "true"}}
	handler.ItemAccess = allowAllPlaybackItemAccess{}

	start := v3HandlerStartRequest()
	start.ClientFeatures = append(start.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3)
	started := startPlayableDecisionV2(t, handler, marshalV3StartRequest(t, start))
	if !playback.HasFeatureV3(started.NegotiatedClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3) {
		t.Fatalf("start negotiated features = %v, want header authentication", started.NegotiatedClientFeatures)
	}

	nextContext := start.ClientPlaybackContext
	nextContext.Output.OutputContextID = "route-2"
	replanned := replanPlaybackDecisionV2(t, handler, started.SessionID, playback.ReplanRequestV3{
		ProtocolVersion:       playback.ProtocolV3,
		ClientFeatures:        []string{}, // explicit attempted downgrade
		Operation:             playback.ReplanOperationOutputChangeV3,
		PlaybackAttemptID:     start.PlaybackAttemptID,
		ReplanRequestID:       "bloem-negotiated-replan-0001",
		FailedPlanID:          started.PlaybackPlan.PlanID,
		PlanAttemptID:         "bloem-negotiated-attempt-0001",
		PlanAttemptKey:        started.PlaybackPlan.PlanAttemptKey,
		AttemptCount:          1,
		PositionSeconds:       12,
		SelectedTracks:        started.PlaybackPlan.SelectedTracks,
		Capabilities:          start.Capabilities,
		ClientPlaybackContext: nextContext,
	})
	if replanned.PlaybackPlan == nil {
		t.Fatalf("replan = %#v", replanned)
	}
	if !playback.HasFeatureV3(replanned.NegotiatedClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3) {
		t.Fatalf("replan negotiated features = %v, want durable header authentication", replanned.NegotiatedClientFeatures)
	}
}
