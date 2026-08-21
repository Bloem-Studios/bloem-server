// Package accesspolicy contains dependency-neutral effective-policy
// evaluation. Request-context and persistence packages adapt their own
// authoritative subjects and providers to these types.
package accesspolicy

import (
	"context"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/google/uuid"
)

const (
	PlaybackQualityStandard = "1080p"
	PlaybackQuality4K       = "2160p"
)

type GroupSubject struct {
	OrganizationID uuid.UUID
	AccountID      int
	ProfileID      string
	Legacy         bool
}

type GroupPolicyProvider interface {
	ResolvePolicy(context.Context, GroupSubject) (*GroupPolicy, error)
}

type GroupPolicy struct {
	ID                       int64
	LibraryIDs               []int
	MaxPlaybackQuality       string
	PlaybackAllowed          bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int
	MaxProfiles              int
	MaxTranscodes            int
	AllowedPermissions       []string
	RequestsAllowed          bool
}

type EffectiveUserPolicy struct {
	LibraryIDs               []int
	MaxPlaybackQuality       string
	PlaybackAllowed          bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int
	MaxProfiles              int
	MaxTranscodes            int
	Permissions              []string
	RequestsAllowed          bool
}

func ParsePlaybackQualityPreset(value string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "", "ANY":
		return "", true
	case "STANDARD", "480P", "720P", "1080P":
		return PlaybackQualityStandard, true
	case "4K", "UHD", "2160P", "4320P":
		return PlaybackQuality4K, true
	default:
		return "", false
	}
}

func NormalizePlaybackQuality(value string) string {
	if normalized, ok := ParsePlaybackQualityPreset(value); ok {
		return normalized
	}
	return ""
}

func NoGroupPolicy() GroupPolicy {
	return GroupPolicy{
		PlaybackAllowed:          true,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: false,
		TranscodeAllowed:         true,
		AudioTranscodeAllowed:    true,
		RequestsAllowed:          true,
	}
}

func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil || user.Role == models.RoleAdmin {
		return ApplyGroupPolicy(user, nil), nil
	}
	group, err := provider.ResolvePolicy(ctx, subject)
	if err != nil {
		return EffectiveUserPolicy{}, err
	}
	return ApplyGroupPolicy(user, group), nil
}

func ApplyGroupPolicy(user *models.User, group *GroupPolicy) EffectiveUserPolicy {
	if user == nil {
		return EffectiveUserPolicy{RequestsAllowed: true}
	}
	base := NoGroupPolicy()
	if group != nil {
		base = *group
	}
	effective := EffectiveUserPolicy{
		LibraryIDs:               inheritLibraryIDs(user.LibraryIDs, base.LibraryIDs),
		MaxPlaybackQuality:       NormalizePlaybackQuality(inheritString(user.MaxPlaybackQuality, base.MaxPlaybackQuality)),
		PlaybackAllowed:          base.PlaybackAllowed,
		DownloadAllowed:          inheritBool(user.DownloadAllowed, base.DownloadAllowed),
		DownloadTranscodeAllowed: inheritBool(user.DownloadTranscodeAllowed, base.DownloadTranscodeAllowed),
		TranscodeAllowed:         inheritBool(user.TranscodeAllowed, base.TranscodeAllowed),
		AudioTranscodeAllowed:    inheritBool(user.AudioTranscodeAllowed, base.AudioTranscodeAllowed),
		MaxStreams:               inheritInt(user.MaxStreams, base.MaxStreams),
		MaxProfiles:              strictestPositive(user.MaxProfiles, base.MaxProfiles),
		MaxTranscodes:            inheritInt(user.MaxTranscodes, base.MaxTranscodes),
		Permissions:              cloneStrings(user.Permissions),
		RequestsAllowed:          inheritBool(user.RequestsAllowed, base.RequestsAllowed),
	}
	if group != nil && group.AllowedPermissions != nil {
		effective.Permissions = intersectStrings(user.Permissions, group.AllowedPermissions)
	}
	return effective
}

func inheritLibraryIDs(userLibraryIDs, groupLibraryIDs []int) []int {
	if userLibraryIDs != nil {
		return sortedUniqueInts(userLibraryIDs)
	}
	if groupLibraryIDs != nil {
		return sortedUniqueInts(groupLibraryIDs)
	}
	return nil
}

func inheritInt(override *int, inherited int) int {
	if override == nil {
		return inherited
	}
	if *override < 0 {
		return 0
	}
	return *override
}

func strictestPositive(account, group int) int {
	if account <= 0 {
		return group
	}
	if group <= 0 || account < group {
		return account
	}
	return group
}

func inheritBool(override *bool, inherited bool) bool {
	if override != nil {
		return *override
	}
	return inherited
}

func inheritString(override *string, inherited string) string {
	if override != nil {
		return *override
	}
	return inherited
}

func intersectStrings(left, right []string) []string {
	if len(left) == 0 || len(right) == 0 {
		return []string{}
	}
	allowed := make(map[string]struct{}, len(right))
	for _, raw := range right {
		value := strings.TrimSpace(raw)
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	seen := make(map[string]struct{}, len(left))
	out := make([]string, 0, len(left))
	for _, raw := range left {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func sortedUniqueInts(values []int) []int {
	if values == nil {
		return nil
	}
	out := append([]int{}, values...)
	sort.Ints(out)
	return slicesCompact(out)
}

func slicesCompact(values []int) []int {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] == values[write-1] {
			continue
		}
		values[write] = values[read]
		write++
	}
	return values[:write]
}
