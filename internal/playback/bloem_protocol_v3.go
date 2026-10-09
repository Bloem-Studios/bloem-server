package playback

import (
	"slices"
	"strings"
)

// NegotiateClientFeaturesV3 returns the known features accepted for one
// playback attempt. Order follows the request, tokens are canonicalized and
// deduplicated, and authorized media origins require header-authenticated media.
func NegotiateClientFeaturesV3(requested []string, surfaceFeatures ...string) []string {
	known := append(ServerFeaturesV3(), FeatureClientVideoTransforms)
	known = append(known, surfaceFeatures...)
	accepted := make([]string, 0, len(requested))
	for _, raw := range requested {
		for _, candidate := range known {
			if strings.EqualFold(strings.TrimSpace(raw), candidate) && !slices.Contains(accepted, candidate) {
				accepted = append(accepted, candidate)
				break
			}
		}
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
