package playback

import (
	"errors"
	"os"
	"testing"
)

func TestBoundTranscodeRejectsRuntimePolicyDrift(t *testing.T) {
	for _, variation := range []string{"hardware fallback", "device selection"} {
		t.Run(variation, func(t *testing.T) {
			ns := executorFixture()
			root := t.TempDir()
			output, _ := ns.OutputDir(root)
			opts := TranscodeOpts{SessionID: "frozen-policy", Executor: &ns, OutputDir: output, FFmpegPath: executorTestBinary(t), ExecuteGrants: executorGrantTestProvider(nil), HWAccel: HWAccelNone, FastStart: true}
			if variation == "hardware fallback" {
				opts.HWAccel = "qsv"
				opts.SourceVideoCodec = "mpeg4"
			} else {
				opts.HWDevice = "/device/a,/device/b"
			}
			if _, err := StartTranscode(t.Context(), opts); !errors.Is(err, ErrFrozenTranscodePolicyChanged) {
				t.Fatalf("runtime drift accepted: %v", err)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected recipe claimed output: %v", err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatalf("rejected recipe left filesystem effects: %v %v", entries, err)
			}
		})
	}
}

func TestPrepareFrozenTranscodeRejectsDeviceSet(t *testing.T) {
	ns := executorFixture()
	if _, err := PrepareFrozenTranscodeOpts(t.Context(), TranscodeOpts{Executor: &ns, HWDevice: "/device/a,/device/b"}); !errors.Is(err, ErrFrozenTranscodePolicyChanged) {
		t.Fatalf("ambiguous frozen device accepted: %v", err)
	}
}

func TestLegacyTranscodeRetainsHardwareFallback(t *testing.T) {
	opts := TranscodeOpts{SessionID: "legacy-policy", OutputDir: t.TempDir(), FFmpegPath: executorTestBinary(t), HWAccel: "qsv", SourceVideoCodec: "mpeg4", FastStart: true}
	session, err := StartTranscode(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close() //nolint:errcheck
	if session.opts.HWAccel != HWAccelNone {
		t.Fatalf("legacy fallback changed: %q", session.opts.HWAccel)
	}
}
