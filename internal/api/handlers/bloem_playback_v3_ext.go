package handlers

import (
	"context"
	"net/http"
	"strings"

	apimw "github.com/Silo-Server/silo-server/internal/api/middleware"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func (h *PlaybackHandler) headerAuthenticatedMediaReady(ctx context.Context) bool {
	if h.SettingsRepo == nil {
		return false
	}
	value, err := h.SettingsRepo.Get(ctx, "playback.header_authenticated_media_mode")
	return err == nil && strings.TrimSpace(value) == "single_or_affine"
}

func decisionResponseForFeaturesV3(response playback.DecisionResponseV3, features []string) playback.DecisionResponseV3 {
	response.ServerFeatures = playback.DeploymentFeaturesV3(playback.HasFeatureV3(features, playback.FeatureHeaderAuthenticatedMediaV3))
	response.NegotiatedClientFeatures = append([]string(nil), features...)
	return response
}

func mediaAuthMetricModeV3(mode mediaAuthModeV3) playback.MediaAuthModeV3 {
	if !mode.headerAuth {
		return playback.MediaAuthLegacy
	}
	if mode.proxyEgress {
		return playback.MediaAuthHeaderProxy
	}
	return playback.MediaAuthHeaderAPI
}

// playbackRequestDeviceIDV3 is the registered device id the starting client
// identified with: the device header first, then the session claims.
func playbackRequestDeviceIDV3(r *http.Request) string {
	if deviceID := deviceMetadataFromRequest(r).DeviceID; deviceID != "" {
		return deviceID
	}
	if claims := apimw.GetClaims(r.Context()); claims != nil {
		return claims.DeviceID
	}
	return ""
}
