package entitlements_test

import (
	"testing"

	"github.com/Silo-Server/silo-server/internal/entitlements"
)

func TestEffectivePolicyDigestIncludesAudioTranscodeGate(t *testing.T) {
	base := entitlements.EffectivePolicySnapshot{
		LibraryIDs: []int{3, 1}, PlaybackAllowed: true, MaxStreams: 4, MaxProfiles: 5,
		TranscodeAllowed: true, MaxTranscodes: 2, DownloadAllowed: true,
		DownloadTranscodeAllowed: true, MaxPlaybackQuality: "2160p",
		AllowedPermissions: []string{"marker_edit"}, RequestsAllowed: true,
	}
	audioAllowed := base
	audioAllowed.AudioTranscodeAllowed = true

	withoutAudio, err := entitlements.EffectivePolicyDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	withAudio, err := entitlements.EffectivePolicyDigest(audioAllowed)
	if err != nil {
		t.Fatal(err)
	}
	if withoutAudio == withAudio {
		t.Fatalf("audio-only policy change produced identical digest %q", withAudio)
	}
}
