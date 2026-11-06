package access

import "testing"

// TestBloemWiderThanComparesPlaybackAndPreview: a policy that may play where
// the limit may not, or a full one where the limit is preview-only, is
// wider; the reverse is not.
func TestBloemWiderThanComparesPlaybackAndPreview(t *testing.T) {
	full := EffectiveUserPolicy{PlaybackAllowed: true}
	for _, c := range []struct {
		name          string
		policy, limit EffectiveUserPolicy
		want          []string
	}{
		{"playback where the limit has none", full, EffectiveUserPolicy{}, []string{"playback_allowed"}},
		{"full where the limit is preview-only", full, EffectiveUserPolicy{PreviewOnly: true}, []string{"playback_allowed", "preview_only"}},
		{"the same", full, full, nil},
		{"narrower", EffectiveUserPolicy{PreviewOnly: true}, full, nil},
	} {
		var got []string
		for _, w := range WiderThan(c.policy, c.limit) {
			if w.Field == "playback_allowed" || w.Field == "preview_only" {
				got = append(got, w.Field)
			}
		}
		if len(got) != len(c.want) || (len(got) > 0 && (got[0] != c.want[0] || got[len(got)-1] != c.want[len(c.want)-1])) {
			t.Errorf("%s: widenings %v, want %v", c.name, got, c.want)
		}
	}
}
