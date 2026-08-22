package adminpeople

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/accesspolicy"
	"github.com/Silo-Server/silo-server/internal/entitlements"
)

func TestManagedEffectivePolicyMatchesEveryTargetField(t *testing.T) {
	targetPolicy := entitlements.Policy{
		LibraryIDs: []int{1, 2}, PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5,
		TranscodeAllowed: true, MaxTranscodes: 2, DownloadAllowed: true,
		DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p",
		AllowedPermissions: []string{"requests.create"}, RequestsAllowed: true,
	}
	target := accesspolicy.EffectiveUserPolicy{
		LibraryIDs: targetPolicy.LibraryIDs, PlaybackAllowed: targetPolicy.PlaybackAllowed,
		MaxStreams: targetPolicy.MaxStreams, MaxProfiles: targetPolicy.MaxProfiles,
		TranscodeAllowed: targetPolicy.TranscodeAllowed, AudioTranscodeAllowed: true,
		MaxTranscodes: targetPolicy.MaxTranscodes, DownloadAllowed: targetPolicy.DownloadAllowed,
		DownloadTranscodeAllowed: targetPolicy.DownloadTranscodeAllowed,
		MaxPlaybackQuality:       targetPolicy.MaxPlaybackQuality,
		Permissions:              targetPolicy.AllowedPermissions, RequestsAllowed: targetPolicy.RequestsAllowed,
	}
	effective := entitlements.EffectivePolicySnapshot{
		LibraryIDs: []int{1, 2}, PlaybackAllowed: true, MaxStreams: 3, MaxProfiles: 5,
		TranscodeAllowed: true, AudioTranscodeAllowed: true, MaxTranscodes: 2,
		DownloadAllowed: true, DownloadTranscodeAllowed: true, MaxPlaybackQuality: "1080p",
		AllowedPermissions: []string{"requests.create"}, RequestsAllowed: true,
	}
	if !managedEffectivePolicyMatchesTarget(effective, target) {
		t.Fatal("complete equal policy did not match")
	}

	tests := []struct {
		name   string
		mutate func(*entitlements.EffectivePolicySnapshot)
	}{
		{name: "libraries nil differs from empty", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.LibraryIDs = nil }},
		{name: "playback", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.PlaybackAllowed = false }},
		{name: "streams", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.MaxStreams = 1 }},
		{name: "profiles", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.MaxProfiles = 1 }},
		{name: "video transcode", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.TranscodeAllowed = false }},
		{name: "audio transcode", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.AudioTranscodeAllowed = false }},
		{name: "transcodes", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.MaxTranscodes = 1 }},
		{name: "download", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.DownloadAllowed = false }},
		{name: "transcoded download", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.DownloadTranscodeAllowed = false }},
		{name: "quality", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.MaxPlaybackQuality = "720p" }},
		{name: "permissions empty differs from nil", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.AllowedPermissions = nil }},
		{name: "requests", mutate: func(policy *entitlements.EffectivePolicySnapshot) { policy.RequestsAllowed = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := effective
			changed.LibraryIDs = append([]int(nil), effective.LibraryIDs...)
			changed.AllowedPermissions = append([]string(nil), effective.AllowedPermissions...)
			test.mutate(&changed)
			if managedEffectivePolicyMatchesTarget(changed, target) {
				t.Fatalf("policy differing only in %s matched target", test.name)
			}
		})
	}
}
