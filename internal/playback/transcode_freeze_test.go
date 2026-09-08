package playback

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestPrepareFrozenTranscodeSelectsLoadedDeviceSet(t *testing.T) {
	resetDeviceLoad(t)
	fakeDeviceStat(t, "/device/a", "/device/b")
	_, releaseBusy := AcquireHWDevice("/device/a", "qsv")
	defer releaseBusy()
	ns := executorFixture()
	opts := TranscodeOpts{Executor: &ns, HWAccel: "qsv", HWDevice: "/device/missing,/device/a,/device/b", SourceVideoCodec: "h264"}
	prepared, err := PrepareFrozenTranscodeOpts(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HWAccel != "qsv" || prepared.HWDevice != "/device/b" {
		t.Fatalf("wrong prepared policy: %s %s", prepared.HWAccel, prepared.HWDevice)
	}
	if hwDeviceActiveCount("/device/a") != 1 || hwDeviceActiveCount("/device/b") != 0 {
		t.Fatal("preparation retained a workload or disturbed active work")
	}
	// A later load change must not move a published recipe to another device.
	_, releaseLater := AcquireHWDevice("/device/b", "qsv")
	defer releaseLater()
	actual, releaseActual := AcquireHWDevice(prepared.HWDevice, prepared.HWAccel)
	defer releaseActual()
	if actual != prepared.HWDevice || hwDeviceActiveCount(actual) != 2 {
		t.Fatal("published device was rebalanced")
	}
}

func TestPrepareFrozenTranscodeRejectsAbsentDeviceSet(t *testing.T) {
	resetDeviceLoad(t)
	fakeDeviceStat(t)
	ns := executorFixture()
	if _, err := PrepareFrozenTranscodeOpts(t.Context(), TranscodeOpts{Executor: &ns, HWAccel: "qsv", HWDevice: "/device/a,/device/b"}); err == nil {
		t.Fatal("missing render devices accepted")
	}
	if len(HWDeviceLoadSnapshot()) != 0 {
		t.Fatal("failed preparation retained workload")
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

func TestPrepareFrozenTranscodeAutoSelectsExistingGPU(t *testing.T) {
	resetDeviceLoad(t)
	env := setupHWAccelTest(t)
	env.addRenderDevice(t, "renderD128", "0x8086")
	env.addRenderDevice(t, "renderD129", "0x8086")
	a, b := filepath.Join(env.driDir, "renderD128"), filepath.Join(env.driDir, "renderD129")
	_, release := AcquireHWDevice(a, "qsv")
	defer release()
	ffmpeg := writeFakeFFmpeg(t, fullyCapableProbe())
	ns := executorFixture()
	prepared, err := PrepareFrozenTranscodeOpts(t.Context(), TranscodeOpts{Executor: &ns, HWAccel: "auto", HWDevice: a + "," + b, FFmpegPath: ffmpeg.path, SourceVideoCodec: "h264", TargetCodecVideo: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.HWAccel != "qsv" || prepared.HWDevice != b {
		t.Fatalf("auto policy = %s %s", prepared.HWAccel, prepared.HWDevice)
	}
	if hwDeviceActiveCount(a) != 1 || hwDeviceActiveCount(b) != 0 {
		t.Fatal("prepare changed active accounting")
	}
}

func TestPreparedMultiGPUProcessesKeepFrozenAssignments(t *testing.T) {
	resetDeviceLoad(t)
	fakeDeviceStat(t, "/device/a", "/device/b")
	root := t.TempDir()
	binary := executorTestBinary(t)
	prepare := func() TranscodeOpts {
		t.Helper()
		ns := executorFixture()
		output, err := ns.OutputDir(root)
		if err != nil {
			t.Fatal(err)
		}
		opts, err := PrepareFrozenTranscodeOpts(t.Context(), TranscodeOpts{SessionID: ns.ExecutorID, Executor: &ns, OutputDir: output, FFmpegPath: binary, ExecuteGrants: executorGrantTestProvider(nil), HWAccel: "qsv", HWDevice: "/device/a,/device/b", SourceVideoCodec: "h264", TargetCodecVideo: "h264", FastStart: true})
		if err != nil {
			t.Fatal(err)
		}
		return opts
	}
	start := func(opts TranscodeOpts) *TranscodeSession {
		t.Helper()
		session, err := StartTranscode(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		if session.opts.HWDevice != opts.HWDevice || session.hwWorkloadDevice != opts.HWDevice {
			t.Fatal("actual process changed frozen device")
		}
		if !strings.Contains(strings.Join(session.cmd.Args, " "), opts.HWDevice) {
			t.Fatal("ffmpeg arguments omit frozen device")
		}
		return session
	}
	firstOpts := prepare()
	first := start(firstOpts)
	secondOpts := prepare()
	if firstOpts.HWDevice == secondOpts.HWDevice {
		t.Fatal("second preparation ignored active first process")
	}
	second := start(secondOpts)
	if hwDeviceActiveCount("/device/a") != 1 || hwDeviceActiveCount("/device/b") != 1 {
		t.Fatal("active processes not counted independently")
	}
	if err := first.Restart(t.Context(), 20, 3); !errors.Is(err, ErrExecutorReplacementRequired) {
		t.Fatalf("bound restart changed authority: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if hwDeviceActiveCount(firstOpts.HWDevice) != 0 || hwDeviceActiveCount(secondOpts.HWDevice) != 1 {
		t.Fatal("first exit disturbed second assignment")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if len(HWDeviceLoadSnapshot()) != 0 {
		t.Fatal("process exits leaked accounting")
	}
}
