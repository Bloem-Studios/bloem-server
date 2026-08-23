package playback

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type MediaAuthModeV3 string

const (
	MediaAuthLegacy      MediaAuthModeV3 = "legacy"
	MediaAuthHeaderAPI   MediaAuthModeV3 = "header_api_origin"
	MediaAuthHeaderProxy MediaAuthModeV3 = "header_authorized_origin"
)

type MediaAuthDowngradeReasonV3 string

const MediaAuthDowngradeDeploymentNotReady MediaAuthDowngradeReasonV3 = "deployment_not_ready"

type MediaAuthFallbackReasonV3 string

const (
	MediaAuthFallbackUnauthorized MediaAuthFallbackReasonV3 = "unauthorized"
	MediaAuthFallbackForbidden    MediaAuthFallbackReasonV3 = "forbidden"
)

var (
	mediaAuthAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "silo_playback_media_auth_attempts_total",
		Help: "Number of committed playback attempts by negotiated media authentication mode.",
	}, []string{"mode"})
	mediaAuthReadinessDowngrades = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "silo_playback_media_auth_readiness_downgrades_total",
		Help: "Number of requested header-authenticated attempts downgraded by a bounded readiness reason.",
	}, []string{"reason"})
	mediaAuthLegacyFallbacks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "silo_playback_media_auth_legacy_fallbacks_total",
		Help: "Number of fresh legacy fallback attempts by a bounded authentication reason.",
	}, []string{"reason"})
)

func RecordMediaAuthAttempt(mode MediaAuthModeV3) bool {
	switch mode {
	case MediaAuthLegacy, MediaAuthHeaderAPI, MediaAuthHeaderProxy:
		mediaAuthAttempts.WithLabelValues(string(mode)).Inc()
		return true
	default:
		return false
	}
}

func RecordMediaAuthReadinessDowngrade(reason MediaAuthDowngradeReasonV3) bool {
	if reason != MediaAuthDowngradeDeploymentNotReady {
		return false
	}
	mediaAuthReadinessDowngrades.WithLabelValues(string(reason)).Inc()
	return true
}

func RecordMediaAuthLegacyFallback(reason MediaAuthFallbackReasonV3) bool {
	switch reason {
	case MediaAuthFallbackUnauthorized, MediaAuthFallbackForbidden:
		mediaAuthLegacyFallbacks.WithLabelValues(string(reason)).Inc()
		return true
	default:
		return false
	}
}
