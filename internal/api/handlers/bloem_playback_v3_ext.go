package handlers

import (
	"context"
	"errors"
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

// bloemNegotiateClientFeaturesV3 narrows the start request's client features
// to what this deployment can serve (header-authenticated media needs a ready
// deployment) and records a readiness downgrade when one was requested.
func (h *PlaybackHandler) bloemNegotiateClientFeaturesV3(ctx context.Context, req *playback.StartRequestV3) {
	requestedClientFeatures := append([]string(nil), req.ClientFeatures...)
	headerAuthReady := h.headerAuthenticatedMediaReady(ctx)
	req.ClientFeatures = playback.NegotiateClientFeaturesV3(req.ClientFeatures, headerAuthReady)
	if playback.HasFeatureV3(requestedClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3) &&
		!playback.HasFeatureV3(req.ClientFeatures, playback.FeatureHeaderAuthenticatedMediaV3) {
		playback.RecordMediaAuthReadinessDowngrade(playback.MediaAuthDowngradeDeploymentNotReady)
	}
}

// bloemCheckStartTranscodingAllowedV3 enforces the per-profile transcoding
// permission before a v3 start creates a session that would transcode.
func (h *PlaybackHandler) bloemCheckStartTranscodingAllowedV3(ctx context.Context, userID int, profileID string, method playback.PlayMethod, transcodeAudio bool) *transportErrorV3 {
	checker, ok := h.sessionMgr.(transcodePermissionChecker)
	if !ok || (method != playback.PlayTranscode && !transcodeAudio) {
		return nil
	}
	if err := checker.CheckTranscodingAllowed(ctx, userID, profileID, method == playback.PlayTranscode); err != nil {
		reason := "transcoding_disabled"
		if errors.Is(err, playback.ErrAudioTranscodingDisabled) {
			reason = "audio_transcoding_disabled"
		}
		return &transportErrorV3{reason: reason, message: "The selected server adaptation is disabled for this user."}
	}
	return nil
}

// bloemBindPlaybackDeviceV3 records the requesting device on the session:
// remote control (S-5a) resolves the device's advertised command list
// through the session; a missing id is not an error.
func (h *PlaybackHandler) bloemBindPlaybackDeviceV3(r *http.Request, sessionID string) {
	if deviceID := playbackRequestDeviceIDV3(r); deviceID != "" {
		if setter, ok := h.sessionMgr.(interface{ SetDeviceID(string, string) error }); ok {
			_ = setter.SetDeviceID(sessionID, deviceID)
		}
	}
}
