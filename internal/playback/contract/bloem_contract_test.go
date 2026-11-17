package contract

// Bloem-owned contract coverage: decision responses carry Bloem's
// negotiated_client_features (playback.DecisionResponseV3).

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

func TestDecisionFixtureCarriesNegotiatedClientFeatures(t *testing.T) {
	var decision struct {
		NegotiatedClientFeatures []string `json:"negotiated_client_features"`
	}
	mustUnmarshalGolden(t, "decision_response.json", &decision)
	assertStringsEqual(t, "decision fixture negotiated_client_features", decision.NegotiatedClientFeatures, []string{playback.FeaturePlaybackPlanV3})
}
