package playback

import (
	"slices"
	"strings"
)

const (
	// FeatureHeaderAuthenticatedMediaReadyV3 is deployment readiness, not
	// binary support. It is advertised only when the administrator has enabled
	// the complete header-authenticated serving path.
	FeatureHeaderAuthenticatedMediaReadyV3 = "header_authenticated_media_ready_v1"
)

// DeploymentFeaturesV3 adds dynamic deployment readiness to the stable
// protocol feature vocabulary. Binary support remains visible while disabled
// so clients can distinguish a rollout setting from an old server.
func DeploymentFeaturesV3(headerAuthReady bool) []string {
	features := ServerFeaturesV3()
	if headerAuthReady {
		features = append(features, FeatureHeaderAuthenticatedMediaReadyV3)
	}
	return features
}

// NegotiateClientFeaturesV3 returns the known features accepted for one
// playback attempt. Order follows the request, tokens are canonicalized and
// deduplicated, and deployment-gated transport features fail closed.
func NegotiateClientFeaturesV3(requested []string, headerAuthReady bool) []string {
	known := append(ServerFeaturesV3(), FeatureClientVideoTransforms)
	accepted := make([]string, 0, len(requested))
	for _, raw := range requested {
		for _, candidate := range known {
			if strings.EqualFold(strings.TrimSpace(raw), candidate) && !slices.Contains(accepted, candidate) {
				accepted = append(accepted, candidate)
				break
			}
		}
	}
	if !headerAuthReady {
		accepted = withoutFeaturesV3(accepted, FeatureHeaderAuthenticatedMediaV3, FeatureAuthorizedMediaOriginsV3)
	}
	if !HasFeatureV3(accepted, FeatureHeaderAuthenticatedMediaV3) {
		accepted = withoutFeaturesV3(accepted, FeatureAuthorizedMediaOriginsV3)
	}
	return accepted
}

func withoutFeaturesV3(features []string, removed ...string) []string {
	filtered := make([]string, 0, len(features))
	for _, feature := range features {
		if !slices.ContainsFunc(removed, func(candidate string) bool { return strings.EqualFold(feature, candidate) }) {
			filtered = append(filtered, feature)
		}
	}
	return filtered
}
