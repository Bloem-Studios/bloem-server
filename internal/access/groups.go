package access

import (
	"context"
	"sort"
	"strings"

	"github.com/Silo-Server/silo-server/internal/models"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// GroupSubject identifies the profile whose organization-owned access group is
// resolved. Legacy is the temporary default-organization compatibility ceiling.
type GroupSubject struct {
	OrganizationID uuid.UUID
	AccountID      int
	ProfileID      string
	Legacy         bool
}

// GroupSubjectFromContext derives a group subject exclusively from a
// server-validated tenant context and the already-authenticated account/profile.
func GroupSubjectFromContext(ctx context.Context, accountID int, profileID string) (GroupSubject, error) {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok || tenant.OrganizationID == uuid.Nil || tenant.AccountID != accountID {
		return GroupSubject{}, ErrGroupNotFound
	}
	return GroupSubject{
		OrganizationID: tenant.OrganizationID,
		AccountID:      accountID,
		ProfileID:      profileID,
		Legacy:         tenant.Legacy,
	}, nil
}

// GroupPolicyProvider loads the access-group restriction layer for a subject.
type GroupPolicyProvider interface {
	ResolvePolicy(context.Context, GroupSubject) (*GroupPolicy, error)
}

// GroupPolicy is the access group's policy layer. Every user-level policy
// field has a group counterpart here; the group value applies to each member
// whose own field is unset (inherits).
type GroupPolicy struct {
	ID                       int64
	LibraryIDs               []int // nil = unrestricted
	MaxPlaybackQuality       string
	PlaybackAllowed          bool
	DownloadAllowed          bool
	DownloadTranscodeAllowed bool
	TranscodeAllowed         bool
	AudioTranscodeAllowed    bool
	MaxStreams               int // 0 = no group cap
	MaxProfiles              int // 0 = no group cap
	MaxTranscodes            int
	AllowedPermissions       []string // nil = all assignable
	RequestsAllowed          bool
}

// EffectiveUserPolicy is the fully resolved policy for an account: every field
// carries a concrete value (user override when set, otherwise the group value,
// otherwise the permissive no-group default).
type EffectiveUserPolicy struct {
	LibraryIDs               []int // nil = unrestricted
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

// NoGroupPolicy is the policy applied to an account with no access group
// (admins are ungrouped). It is permissive so that an unset field on such an
// account keeps today's unrestricted behavior. DownloadTranscodeAllowed is
// the exception: it defaults to false because that was the old column default
// on users (and is the seeded Default Group's value), so an account that never
// had the gate turned on does not silently gain it.
func NoGroupPolicy() GroupPolicy {
	return GroupPolicy{
		LibraryIDs:               nil,
		MaxPlaybackQuality:       "",
		PlaybackAllowed:          true,
		DownloadAllowed:          true,
		DownloadTranscodeAllowed: false,
		TranscodeAllowed:         true,
		AudioTranscodeAllowed:    true,
		MaxStreams:               0,
		MaxProfiles:              0,
		MaxTranscodes:            0,
		AllowedPermissions:       nil,
		RequestsAllowed:          true,
	}
}

// GroupApplies reports whether an access group contributes to the user's
// effective policy. Admin accounts are never capped by a group: the repository
// keeps them ungrouped, and a row that still carries a group (written before
// that rule existed) is resolved as if it did not.
func GroupApplies(user *models.User) bool {
	return user != nil && user.AccessGroupID != nil && user.Role != models.RoleAdmin
}

// EffectivePolicyForSubject loads a subject's group policy and returns the
// merged restriction layer. Nil providers are treated as "no group". Unlike
// GroupApplies' AccessGroupID check (correct for the legacy per-row reads it
// guards), this deliberately does NOT skip the provider just because the
// passed-in user has no AccessGroupID set: group membership here is resolved
// fresh per subject by the provider's own LEFT JOIN (see
// GroupStore.ResolvePolicy), not by that Go-level field, so a caller that
// constructs a bare *models.User is still resolved correctly. Only the
// role check is skipped locally -- admins are never capped by a group,
// so there is no reason to spend the query.
func EffectivePolicyForSubject(
	ctx context.Context,
	user *models.User,
	subject GroupSubject,
	provider GroupPolicyProvider,
) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil || user.Role == models.RoleAdmin {
		return ApplyGroupPolicy(user, nil), nil
	}
	group, err := provider.ResolvePolicy(ctx, subject)
	if err != nil {
		return EffectiveUserPolicy{}, err
	}
	return ApplyGroupPolicy(user, group), nil
}

// EffectivePolicyForUser loads a user's group policy and returns the resolved
// policy from the request's server-validated tenant context, for call sites
// with no profile in scope. Nil providers, a nil user, or a request with no
// resolvable tenant context are all treated as "no group" (the permissive
// default), matching EffectivePolicyForSubject's own nil-provider behavior.
func EffectivePolicyForUser(ctx context.Context, user *models.User, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil {
		return ApplyGroupPolicy(user, nil), nil
	}
	subject, err := GroupSubjectFromContext(ctx, user.ID, "")
	if err != nil {
		return ApplyGroupPolicy(user, nil), nil //nolint:nilerr // deliberate fallback, see doc comment above
	}
	return EffectivePolicyForSubject(ctx, user, subject, provider)
}

// ApplyGroupPolicy resolves the user's account policy against the optional
// access group: each field takes the user's explicit override when set and
// the group's value otherwise. A nil group means the permissive
// NoGroupPolicy. Permissions are the one mask-style field: the group's
// allowed_permissions (when set) intersects the user's permissions.
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
	if override != nil {
		if *override < 0 {
			return 0
		}
		return *override
	}
	return inherited
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
		if value == "" {
			continue
		}
		allowed[value] = struct{}{}
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
	out := make([]string, len(values))
	copy(out, values)
	return out
}
