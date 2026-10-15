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

// PreviewTemplateKey is the reserved, protected entitlement template whose
// materialized groups make their accounts preview principals: they may browse
// the catalog and nothing else. The migration that seeds it also pins its
// policy with CHECK constraints, so the key alone is the preview signal.
const PreviewTemplateKey = "bloem-preview"

type GroupSubject struct {
	OrganizationID uuid.UUID
	AccountID      int
	ProfileID      string
	Legacy         bool
}

// GroupPolicyProvider is Silo's access-group policy source: it reads an
// account's group.
type GroupPolicyProvider interface {
	GetPolicyForUser(ctx context.Context, userID int) (*GroupPolicy, error)
}

// SubjectPolicyProvider is Bloem's tenant-scoped extension. A provider that
// implements it resolves the group for an organization, account and profile,
// and callers must supply a server-validated tenant subject.
type SubjectPolicyProvider interface {
	ResolvePolicy(context.Context, GroupSubject) (*GroupPolicy, error)
}

type GroupPolicy struct {
	ID                         int64
	LibraryIDs                 []int
	MaxPlaybackQuality         string
	PlaybackAllowed            bool
	DownloadAllowed            bool
	DownloadTranscodeAllowed   bool
	TranscodeAllowed           bool
	AudioTranscodeAllowed      bool
	MaxStreams                 int
	MaxProfiles                int
	MaxTranscodes              int
	MaxRemoteStreamBitrateKbps int
	MaxLocalStreamBitrateKbps  int
	AllowedPermissions         []string
	RequestsAllowed            bool
	ManagedProfileLimit        bool `json:"-"`
	// PreviewOnly marks a group materialized from PreviewTemplateKey. Its
	// accounts browse and nothing else; account overrides do not apply.
	PreviewOnly bool
}

type EffectiveUserPolicy struct {
	LibraryIDs                 []int
	MaxPlaybackQuality         string
	PlaybackAllowed            bool
	DownloadAllowed            bool
	DownloadTranscodeAllowed   bool
	TranscodeAllowed           bool
	AudioTranscodeAllowed      bool
	MaxStreams                 int
	MaxProfiles                int
	MaxTranscodes              int
	MaxRemoteStreamBitrateKbps int
	MaxLocalStreamBitrateKbps  int
	Permissions                []string
	RequestsAllowed            bool
	// PreviewOnly marks a group materialized from PreviewTemplateKey. Its
	// accounts browse and nothing else; account overrides do not apply.
	PreviewOnly bool
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

const DefaultMaxProfiles = 5

func NoGroupPolicy() GroupPolicy {
	return GroupPolicy{
		PlaybackAllowed:          true,
		MaxProfiles:              DefaultMaxProfiles,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: false,
		TranscodeAllowed:         true,
		AudioTranscodeAllowed:    true,
		RequestsAllowed:          true,
	}
}

// AdminPolicy supplies full defaults beneath an admin account’s explicit overrides.
func AdminPolicy() GroupPolicy {
	policy := NoGroupPolicy()
	policy.DownloadTranscodeAllowed = true
	return policy
}

func EffectivePolicyForSubject(ctx context.Context, user *models.User, subject GroupSubject, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil || user.Role == models.RoleAdmin {
		return ApplyGroupPolicy(user, nil), nil
	}
	var group *GroupPolicy
	var err error
	if scoped, ok := provider.(SubjectPolicyProvider); ok {
		group, err = scoped.ResolvePolicy(ctx, subject)
	} else {
		group, err = provider.GetPolicyForUser(ctx, subject.AccountID)
	}
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
	if user.Role == models.RoleAdmin {
		base, group = AdminPolicy(), nil
	}
	if group != nil {
		base = *group
	}
	if group != nil && group.PreviewOnly {
		// A preview principal's policy is the protected template's, whatever
		// its account says: an override must not widen libraries or switch a
		// capability back on.
		var libraries []int
		if base.LibraryIDs != nil {
			libraries = sortedUniqueInts(base.LibraryIDs)
		}
		return EffectiveUserPolicy{
			LibraryIDs:         libraries,
			MaxPlaybackQuality: NormalizePlaybackQuality(base.MaxPlaybackQuality),
			MaxProfiles:        base.MaxProfiles,
			Permissions:        []string{},
			PreviewOnly:        true,
		}
	}
	effective := EffectiveUserPolicy{
		LibraryIDs:                 inheritLibraryIDs(user.LibraryIDs, base.LibraryIDs),
		MaxPlaybackQuality:         NormalizePlaybackQuality(inheritString(user.MaxPlaybackQuality, base.MaxPlaybackQuality)),
		PlaybackAllowed:            base.PlaybackAllowed,
		DownloadAllowed:            inheritBool(user.DownloadAllowed, base.DownloadAllowed),
		DownloadTranscodeAllowed:   inheritBool(user.DownloadTranscodeAllowed, base.DownloadTranscodeAllowed),
		TranscodeAllowed:           inheritBool(user.TranscodeAllowed, base.TranscodeAllowed),
		AudioTranscodeAllowed:      inheritBool(user.AudioTranscodeAllowed, base.AudioTranscodeAllowed),
		MaxStreams:                 inheritInt(user.MaxStreams, base.MaxStreams),
		MaxProfiles:                inheritInt(user.MaxProfiles, base.MaxProfiles),
		MaxTranscodes:              inheritInt(user.MaxTranscodes, base.MaxTranscodes),
		MaxRemoteStreamBitrateKbps: inheritInt(user.MaxRemoteStreamBitrateKbps, base.MaxRemoteStreamBitrateKbps),
		MaxLocalStreamBitrateKbps:  inheritInt(user.MaxLocalStreamBitrateKbps, base.MaxLocalStreamBitrateKbps),
		Permissions:                cloneStrings(user.Permissions),
		RequestsAllowed:            inheritBool(user.RequestsAllowed, base.RequestsAllowed),
	}
	if group != nil && group.AllowedPermissions != nil {
		effective.Permissions = intersectStrings(user.Permissions, group.AllowedPermissions)
	}
	if group != nil && group.ManagedProfileLimit {
		ceiling := group.MaxProfiles
		if ceiling == 0 {
			ceiling = 1
		}
		effective.MaxProfiles = strictestPositive(effective.MaxProfiles, ceiling)
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
