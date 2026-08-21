package entitlements

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/permissioncatalog"
)

const (
	PolicySetAdd          = "add"
	PolicySetRemove       = "remove"
	PolicySetReplace      = "replace"
	PolicyLibrariesAll    = "all"
	PolicyLibrariesNone   = "none"
	PolicySetUnrestricted = "unrestricted"
)

// SetOperation applies one explicit operation to a policy set. Omitted policy
// patch fields remain unchanged; set operations never toggle current state.
type SetOperation[T any] struct {
	Mode   string `json:"mode"`
	Values []T    `json:"values,omitempty"`
}

// PolicyPatch describes deterministic changes to a complete effective policy.
type PolicyPatch struct {
	Libraries                *SetOperation[int]    `json:"libraries,omitempty"`
	Permissions              *SetOperation[string] `json:"permissions,omitempty"`
	PlaybackAllowed          *bool                 `json:"playback_allowed,omitempty"`
	TranscodeAllowed         *bool                 `json:"transcode_allowed,omitempty"`
	DownloadAllowed          *bool                 `json:"download_allowed,omitempty"`
	DownloadTranscodeAllowed *bool                 `json:"download_transcode_allowed,omitempty"`
	RequestsAllowed          *bool                 `json:"requests_allowed,omitempty"`
	MaxStreams               *int                  `json:"max_streams,omitempty"`
	MaxProfiles              *int                  `json:"max_profiles,omitempty"`
	MaxTranscodes            *int                  `json:"max_transcodes,omitempty"`
	MaxPlaybackQuality       *string               `json:"max_playback_quality,omitempty"`
}

// ApplyPolicyPatch applies an explicit patch and returns the canonical full
// policy. It does not materialize dynamic all-library policies; stores do that
// inside their database snapshot after applying the patch.
func ApplyPolicyPatch(base Policy, patch PolicyPatch) (Policy, error) {
	policy := clonePolicy(base)
	canonical, err := canonicalPolicyPatch(patch)
	if err != nil {
		return Policy{}, err
	}
	if canonical.Libraries != nil {
		policy.LibraryIDs, err = patchIntegerSet(policy.LibraryIDs, *canonical.Libraries)
		if err != nil {
			return Policy{}, err
		}
	}
	if canonical.Permissions != nil {
		policy.AllowedPermissions, err = patchStringSet(policy.AllowedPermissions, *canonical.Permissions)
		if err != nil {
			return Policy{}, err
		}
	}
	if canonical.PlaybackAllowed != nil {
		policy.PlaybackAllowed = *canonical.PlaybackAllowed
	}
	if canonical.TranscodeAllowed != nil {
		policy.TranscodeAllowed = *canonical.TranscodeAllowed
	}
	if canonical.DownloadAllowed != nil {
		policy.DownloadAllowed = *canonical.DownloadAllowed
	}
	if canonical.DownloadTranscodeAllowed != nil {
		policy.DownloadTranscodeAllowed = *canonical.DownloadTranscodeAllowed
	}
	if canonical.RequestsAllowed != nil {
		policy.RequestsAllowed = *canonical.RequestsAllowed
	}
	if canonical.MaxStreams != nil {
		policy.MaxStreams = *canonical.MaxStreams
	}
	if canonical.MaxProfiles != nil {
		policy.MaxProfiles = *canonical.MaxProfiles
	}
	if canonical.MaxTranscodes != nil {
		policy.MaxTranscodes = *canonical.MaxTranscodes
	}
	if canonical.MaxPlaybackQuality != nil {
		policy.MaxPlaybackQuality = *canonical.MaxPlaybackQuality
	}
	return normalizeResolvedPolicy(policy)
}

// PolicyPatchDigest returns a deterministic digest for semantically equivalent
// patch documents. It is suitable for confirmation and idempotency binding.
func PolicyPatchDigest(patch PolicyPatch) (string, error) {
	canonical, err := canonicalPolicyPatch(patch)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("entitlements: encode policy patch digest: %w", err)
	}
	return sha256Hex(payload), nil
}

// PolicyDigest returns a deterministic digest for one canonical full policy.
func PolicyDigest(policy Policy) (string, error) {
	normalized, err := normalizeResolvedPolicy(policy)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("entitlements: encode policy digest: %w", err)
	}
	return sha256Hex(payload), nil
}

// PolicyEqual compares canonical policies while preserving the semantic
// distinction between unrestricted/all and explicit empty sets.
func PolicyEqual(left, right Policy) bool {
	left, leftErr := normalizeResolvedPolicy(left)
	right, rightErr := normalizeResolvedPolicy(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(left, right)
}

func canonicalPolicyPatch(patch PolicyPatch) (PolicyPatch, error) {
	for _, limit := range []*int{patch.MaxStreams, patch.MaxProfiles, patch.MaxTranscodes} {
		if limit != nil && *limit < 0 {
			return PolicyPatch{}, fmt.Errorf("%w: limits cannot be negative", ErrInvalidPolicy)
		}
	}
	if patch.Libraries != nil {
		operation := SetOperation[int]{Mode: strings.TrimSpace(patch.Libraries.Mode), Values: append([]int(nil), patch.Libraries.Values...)}
		for _, value := range operation.Values {
			if value <= 0 {
				return PolicyPatch{}, fmt.Errorf("%w: library ids must be positive", ErrInvalidPolicy)
			}
		}
		sort.Ints(operation.Values)
		operation.Values = deduplicateSorted(operation.Values)
		switch operation.Mode {
		case PolicySetAdd, PolicySetRemove, PolicySetReplace:
		case PolicyLibrariesAll, PolicyLibrariesNone:
			operation.Values = nil
		default:
			return PolicyPatch{}, fmt.Errorf("%w: unsupported library patch mode", ErrInvalidPolicy)
		}
		patch.Libraries = &operation
	}
	if patch.Permissions != nil {
		operation := SetOperation[string]{Mode: strings.TrimSpace(patch.Permissions.Mode), Values: append([]string(nil), patch.Permissions.Values...)}
		for index := range operation.Values {
			operation.Values[index] = strings.TrimSpace(operation.Values[index])
			if !permissioncatalog.IsAssignable(operation.Values[index]) {
				return PolicyPatch{}, fmt.Errorf("%w: unsupported permission", ErrInvalidPolicy)
			}
		}
		sort.Strings(operation.Values)
		operation.Values = deduplicateSorted(operation.Values)
		switch operation.Mode {
		case PolicySetAdd, PolicySetRemove, PolicySetReplace:
		case PolicySetUnrestricted:
			operation.Values = nil
		default:
			return PolicyPatch{}, fmt.Errorf("%w: unsupported permission patch mode", ErrInvalidPolicy)
		}
		patch.Permissions = &operation
	}
	return patch, nil
}

func patchIntegerSet(current []int, operation SetOperation[int]) ([]int, error) {
	switch operation.Mode {
	case PolicySetAdd:
		return append(append([]int{}, current...), operation.Values...), nil
	case PolicySetRemove:
		removed := make(map[int]struct{}, len(operation.Values))
		for _, value := range operation.Values {
			removed[value] = struct{}{}
		}
		result := make([]int, 0, len(current))
		for _, value := range current {
			if _, ok := removed[value]; !ok {
				result = append(result, value)
			}
		}
		return result, nil
	case PolicySetReplace:
		return append([]int{}, operation.Values...), nil
	case PolicyLibrariesAll:
		return nil, nil
	case PolicyLibrariesNone:
		return []int{}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported library patch mode", ErrInvalidPolicy)
	}
}

func patchStringSet(current []string, operation SetOperation[string]) ([]string, error) {
	switch operation.Mode {
	case PolicySetAdd:
		if current == nil {
			return nil, nil
		}
		return append(append([]string{}, current...), operation.Values...), nil
	case PolicySetRemove:
		if current == nil {
			current = permissioncatalog.Assignable()
		}
		removed := make(map[string]struct{}, len(operation.Values))
		for _, value := range operation.Values {
			removed[value] = struct{}{}
		}
		result := make([]string, 0, len(current))
		for _, value := range current {
			if _, ok := removed[value]; !ok {
				result = append(result, value)
			}
		}
		return result, nil
	case PolicySetReplace:
		return append([]string{}, operation.Values...), nil
	case PolicySetUnrestricted:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: unsupported permission patch mode", ErrInvalidPolicy)
	}
}

func clonePolicy(policy Policy) Policy {
	if policy.LibraryIDs != nil {
		policy.LibraryIDs = append([]int{}, policy.LibraryIDs...)
	}
	if policy.AllowedPermissions != nil {
		policy.AllowedPermissions = append([]string{}, policy.AllowedPermissions...)
	}
	return policy
}

func deduplicateSorted[T comparable](values []T) []T {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
