package access

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/models"
)

const DefaultMaxProfiles = accesspolicy.DefaultMaxProfiles

func NoGroupPolicy() GroupPolicy { return accesspolicy.NoGroupPolicy() }

// AdminPolicy preserves upstream account defaults through Bloem’s shared evaluator.
func AdminPolicy() GroupPolicy { return accesspolicy.AdminPolicy() }

// GroupApplies reports whether an access group contributes to the user's
// effective policy. Admin accounts are never capped by a group: the repository
// keeps them ungrouped, and a row that still carries a group (written before
// that rule existed) is resolved as if it did not.
func GroupApplies(user *models.User) bool {
	return user != nil && user.AccessGroupID != nil && user.Role != models.RoleAdmin
}

// EffectivePolicyForUser loads a user's group policy. Silo's account-level
// providers keep Silo's semantics; a tenant-scoped provider resolves from the
// validated request tenant, preserving the legacy no-context fallback.
func EffectivePolicyForUser(ctx context.Context, user *models.User, provider GroupPolicyProvider) (EffectiveUserPolicy, error) {
	if provider == nil || user == nil {
		return ApplyGroupPolicy(user, nil), nil
	}
	if _, scoped := provider.(accesspolicy.SubjectPolicyProvider); !scoped {
		return EffectivePolicyForRequest(ctx, user, "", provider)
	}
	subject, err := GroupSubjectFromContext(ctx, user.ID, "")
	if err != nil {
		if tenantPolicyRequired(ctx) {
			return EffectiveUserPolicy{}, err
		}
		return ApplyGroupPolicy(user, nil), nil //nolint:nilerr // deliberate compatibility fallback
	}
	return EffectivePolicyForSubject(ctx, user, subject, provider)
}

// ApplyGroupPolicy resolves the user's account policy against the optional
// access group: each field takes the user's explicit override when set and
// the group's value otherwise. A nil group means the permissive
// NoGroupPolicy. An admin account resolves against AdminPolicy and ignores
// any group. Permissions are the one mask-style field: the group's
// allowed_permissions (when set) intersects the user's permissions.
func ApplyGroupPolicy(user *models.User, group *GroupPolicy) EffectiveUserPolicy {
	return accesspolicy.ApplyGroupPolicy(user, group)
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

// unlimitedValue renders a policy field's open value.
const unlimitedValue = "unlimited"

// PolicyWidening is one field where a policy grants more than a limit does.
type PolicyWidening struct {
	// Field is the policy field, named as EffectiveUserPolicy's JSON
	// projections name it (library_ids, max_streams, ...).
	Field string
	// Policy and Limit render each side's value; "unlimited" and
	// "unrestricted" spell out the open values.
	Policy string
	Limit  string
}

// WiderThan lists every field where policy grants more than limit: a library
// limit lifted or a library added, a higher or lifted quality ceiling, a
// higher or lifted stream, transcode, or bitrate cap (0 is unlimited), more
// household profiles, a permission or capability limit lacks. Both sides
// must name libraries by the same ids. Nil means policy grants nothing
// limit does not.
func WiderThan(policy, limit EffectiveUserPolicy) []PolicyWidening {
	var out []PolicyWidening
	add := func(field, p, l string) { out = append(out, PolicyWidening{Field: field, Policy: p, Limit: l}) }
	libraries := func(ids []int) string {
		if ids == nil {
			return "unrestricted"
		}
		var parts []string
		for _, id := range sortedUniqueInts(ids) {
			parts = append(parts, strconv.Itoa(id))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	if limit.LibraryIDs != nil {
		allowed := map[int]bool{}
		for _, id := range limit.LibraryIDs {
			allowed[id] = true
		}
		wider := policy.LibraryIDs == nil
		for _, id := range policy.LibraryIDs {
			wider = wider || !allowed[id]
		}
		if wider {
			add("library_ids", libraries(policy.LibraryIDs), libraries(limit.LibraryIDs))
		}
	}
	quality := func(q string) string {
		if q == "" {
			return unlimitedValue
		}
		return q
	}
	if p, l := NormalizePlaybackQuality(policy.MaxPlaybackQuality), NormalizePlaybackQuality(limit.MaxPlaybackQuality); l != "" && (p == "" || CompareQuality(p, l) > 0) {
		add("max_playback_quality", quality(p), quality(l))
	}
	capped := func(field string, p, l int) {
		if l > 0 && (p <= 0 || p > l) {
			render := func(n int) string {
				if n <= 0 {
					return unlimitedValue
				}
				return strconv.Itoa(n)
			}
			add(field, render(p), render(l))
		}
	}
	capped("max_streams", policy.MaxStreams, limit.MaxStreams)
	capped("max_transcodes", policy.MaxTranscodes, limit.MaxTranscodes)
	capped("max_remote_stream_bitrate_kbps", policy.MaxRemoteStreamBitrateKbps, limit.MaxRemoteStreamBitrateKbps)
	capped("max_local_stream_bitrate_kbps", policy.MaxLocalStreamBitrateKbps, limit.MaxLocalStreamBitrateKbps)
	if policy.MaxProfiles > limit.MaxProfiles {
		add("max_profiles", strconv.Itoa(policy.MaxProfiles), strconv.Itoa(limit.MaxProfiles))
	}
	allowed := func(field string, p, l bool) {
		if p && !l {
			add(field, "allowed", "not allowed")
		}
	}
	allowed("download_allowed", policy.DownloadAllowed, limit.DownloadAllowed)
	allowed("download_transcode_allowed", policy.DownloadTranscodeAllowed, limit.DownloadTranscodeAllowed)
	allowed("transcode_allowed", policy.TranscodeAllowed, limit.TranscodeAllowed)
	allowed("audio_transcode_allowed", policy.AudioTranscodeAllowed, limit.AudioTranscodeAllowed)
	allowed("requests_allowed", policy.RequestsAllowed, limit.RequestsAllowed)
	granted := map[string]bool{}
	for _, p := range limit.Permissions {
		granted[strings.TrimSpace(p)] = true
	}
	var extra []string
	for _, p := range policy.Permissions {
		if p = strings.TrimSpace(p); p != "" && !granted[p] && !slices.Contains(extra, p) {
			extra = append(extra, p)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		add("permissions", strings.Join(extra, ", "), "not granted")
	}
	return out
}
