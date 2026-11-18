package playback

import (
	"slices"
	"strconv"
	"testing"
)

// The arguments Bloem's AirPlay plans need from a Dolby encode: the codec, the
// plan's rate or the default, and the plan's channel count when it named one.
func wantDolbyArgs(codec string, defaultKbps int, opts TranscodeOpts) []string {
	kbps := defaultKbps
	if opts.TargetAudioBitrateKbps > 0 {
		kbps = opts.TargetAudioBitrateKbps
	}
	args := []string{"-c:a", codec, "-b:a", strconv.Itoa(kbps) + "k"}
	if opts.TargetAudioChannels > 0 {
		args = append(args, "-ac", strconv.Itoa(opts.TargetAudioChannels))
	}
	return args
}

// Silo's Dolby encode lines in appendAudioArgs, followed by Bloem's targets,
// produce the plan's rate and channels, and Silo's own arguments when the plan
// froze neither.
func TestDolbyEncodeKeepsSilosLineAndAppliesPlanTargets(t *testing.T) {
	for _, tc := range []struct {
		codec string
		kbps  int
	}{{"eac3", 384}, {"ac3", 448}} {
		for _, opts := range []TranscodeOpts{
			{TargetCodecAudio: tc.codec},
			{TargetCodecAudio: tc.codec, TargetAudioBitrateKbps: 640},
			{TargetCodecAudio: tc.codec, TargetAudioChannels: 6},
			{TargetCodecAudio: tc.codec, TargetAudioBitrateKbps: 256, TargetAudioChannels: 2},
		} {
			got := appendAudioArgs(nil, opts)
			if want := wantDolbyArgs(tc.codec, tc.kbps, opts); !slices.Equal(got, want) {
				t.Errorf("%s %+v: args = %v, want %v", tc.codec, opts, got, want)
			}
		}
	}
}
