package playback_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func nativeInputFixture(t *testing.T, budgets ...int64) (playback.TranscodeOpts, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	source := filepath.Join(t.TempDir(), "synthetic.mp4")
	cmd := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-t", "4", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", "-movflags", "+faststart", source)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic fixture: %v %s", err, data)
	}
	opens, released := &atomic.Int32{}, &atomic.Int32{}
	budget := int64(8 << 20)
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	opts := playback.TranscodeOpts{InputPath: "native-storage:synthetic-pinned-ref", InputFormat: "mov", OutputDir: t.TempDir(), FFmpegPath: bin, TargetCodecVideo: "copy", TargetCodecAudio: "copy", HWAccel: "none", SegmentDuration: 1, TotalDuration: 4}
	opts.InputSource = &playback.TranscodeInputSource{Open: func(ctx context.Context, durable playback.TranscodeOpts) (playback.TranscodeInput, error) {
		if durable.InputPath != "native-storage:synthetic-pinned-ref" {
			return nil, errors.New("lost durable identity")
		}
		lifetime, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(lifetime, source)
		if err != nil {
			cancel()
			return nil, err
		}
		input, err := nativestorage.NewToolInput(lifetime, file, nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, ReleaseHost: func() { released.Add(1) }, MaxBytes: budget, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 10 * time.Second})
		if err != nil {
			cancel()
			_ = file.Close()
			return nil, err
		}
		opens.Add(1)
		return input, nil
	}}
	return opts, opens, released
}

func waitNativeInput(t *testing.T, session *playback.TranscodeSession) {
	t.Helper()
	select {
	case <-session.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("ffmpeg/input cleanup did not join")
	}
}

// A lost factory or persisting the execution-only argument breaks recovery.
func TestNativeInputRealFFmpegReopensOnRestartWithoutPersistingGrant(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatalf("native ffmpeg: %v", err)
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, "stream.m3u8")); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 1 || released.Load() != 1 {
		t.Fatalf("input lifetime opens=%d releases=%d", opens.Load(), released.Load())
	}
	if err := session.Restart(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 2 || released.Load() != 2 {
		t.Fatalf("restart input lifetime opens=%d releases=%d", opens.Load(), released.Load())
	}
	if session.Opts().InputPath != opts.InputPath {
		t.Fatal("session retained ephemeral input")
	}
	card := playback.NewRecipeCard(1, "profile", 1, "", session.Opts())
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "http://") || card.InputPath != opts.InputPath {
		t.Fatal("recipe persisted private bridge argument")
	}
}

func TestNativeInputStartFailureJoinsSource(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	opts.FFmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	if _, err := playback.StartTranscode(context.Background(), opts); err == nil {
		t.Fatal("missing executable accepted")
	}
	if opens.Load() != 1 || released.Load() != 1 {
		t.Fatalf("spawn failure leaked input: opens=%d releases=%d", opens.Load(), released.Load())
	}
}

func TestNativeInputRejectsUnreviewedDemuxerBeforeOpen(t *testing.T) {
	opts, opens, _ := nativeInputFixture(t)
	opts.InputFormat = "hls"
	if _, err := playback.StartTranscode(context.Background(), opts); err == nil {
		t.Fatal("playlist demuxer accepted")
	}
	if opens.Load() != 0 {
		t.Fatal("opened source for unreviewed demuxer")
	}
}

func TestNativeInputRealFFmpegVideoEncode(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	opts.TargetCodecVideo = "h264"
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
	if released.Load() != 1 {
		t.Fatal("encode did not release input before Done")
	}
	if _, err := os.Stat(filepath.Join(opts.OutputDir, "seg_00000.ts")); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInputContextBindingChecksSessionIdentity(t *testing.T) {
	opts, opens, _ := nativeInputFixture(t)
	source := opts.InputSource
	opts.InputSource, opts.InputFormat, opts.SessionID = nil, "", "worker-transport"
	ctx := playback.WithTranscodeInputSource(context.Background(), playback.TranscodeInputBinding{InputPath: opts.InputPath, InputFormat: "mov", InputSource: source, SessionID: "another-transport"})
	if _, err := playback.StartTranscode(ctx, opts); err == nil {
		t.Fatal("source granted to a different transport")
	}
	if opens.Load() != 0 {
		t.Fatal("mismatched transport opened source")
	}
	ctx = playback.WithTranscodeInputSource(context.Background(), playback.TranscodeInputBinding{InputPath: opts.InputPath, InputFormat: "mov", InputSource: source, SessionID: opts.SessionID})
	session, err := playback.StartTranscode(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInputSourceRemuxStreamsRealMP4AndJoins(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/synthetic-remux", nil)
	if err := playback.ServeSourceRemux(response, request, opts, "mp4", false, 0, playback.RemuxServeOptions{DVMode: playback.RemuxDVPreserveV3, FFmpegPath: opts.FFmpegPath}); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ftyp") || response.Body.Len() < 100 {
		t.Fatalf("missing MP4 output: %d/%d", response.Code, response.Body.Len())
	}
	if released.Load() != 1 {
		t.Fatal("remux returned before input cleanup")
	}
}

func TestNativeInputCopySeekUsesPinnedToolSource(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	anchor, segment, err := playback.ResolveSourceCopySeekAnchor(context.Background(), opts.FFmpegPath, opts.InputPath, opts.InputSource, opts.InputFormat, 2.5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if anchor < 1.99 || anchor > 2.01 || segment != 2 {
		t.Fatalf("seek origin: %.4f segment=%d", anchor, segment)
	}
	if released.Load() != 1 {
		t.Fatal("copy probe did not join source")
	}
}

func TestNativeInputManagerRestoresFactoryFromDurableCompanion(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	opts.SessionID = "reconstructed-player"
	card := playback.NewRecipeCard(1, "profile", 1, "", opts)
	manager := playback.NewTranscodeManager()
	manager.Config = func() playback.TranscodeRuntimeConfig {
		return playback.TranscodeRuntimeConfig{TranscodeDir: t.TempDir(), FFmpegPath: opts.FFmpegPath, HWAccel: "none"}
	}
	manager.RestoreInputSource = func(_ context.Context, frozen *playback.RecipeCard, restored *playback.TranscodeOpts) error {
		if frozen.InputPath != opts.InputPath {
			return errors.New("durable identity changed")
		}
		restored.InputSource, restored.InputFormat = opts.InputSource, opts.InputFormat
		return nil
	}
	session, err := manager.ReconstructTranscodeWithError(context.Background(), opts.SessionID, -1, card)
	if err != nil || session == nil {
		t.Fatalf("reconstruct: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
	if opens.Load() != 1 || released.Load() != 1 || session.Opts().InputPath != opts.InputPath {
		t.Fatal("reconstruction lost input identity/lifetime")
	}
}

func TestNativeInputDVRPUZeroExitDoesNotHidePacketRejection(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	tool := filepath.Join(t.TempDir(), "synthetic-strip-tool")
	script := "#!/bin/sh\ncase \" $* \" in *\" -bsfs \"*) printf 'dovi_rpu\\nfilter_units\\n'; exit 0;; esac\nprintf 'Error applying bitstream filters to a packet' >&2\nexit 0\n"
	if err := os.WriteFile(tool, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	allowed, err := playback.SourceDVRPUStrippable(context.Background(), tool, opts.InputPath, opts.InputSource, opts.InputFormat)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || released.Load() != 1 {
		t.Fatal("zero exit hid rejected strip/input leak")
	}
}

func TestNativeInputFailedGenerationCannotServeExistingArtifacts(t *testing.T) {
	opts, _, _ := nativeInputFixture(t, 1)
	opts.TargetCodecVideo = "h264"
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if session.WaitError() == nil {
		t.Fatal("source failure not observed")
	}
	if err := os.WriteFile(filepath.Join(opts.OutputDir, "seg_00000.ts"), []byte("untrusted partial generation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.OutputDir, "stream.m3u8"), []byte("#EXTM3U\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := session.OpenSegment("seg_00000.ts"); !errors.Is(err, playback.ErrTranscodeFailed) {
		t.Fatalf("failed source segment admitted: %v", err)
	}
	if _, err := session.GetSegment("seg_00000.ts"); !errors.Is(err, playback.ErrTranscodeFailed) {
		t.Fatalf("failed source path admitted: %v", err)
	}
	if _, err := session.WaitForSegment("seg_00000.ts", time.Second); !errors.Is(err, playback.ErrTranscodeFailed) {
		t.Fatalf("failed source wait admitted: %v", err)
	}
	if _, err := session.GetManifest(); !errors.Is(err, playback.ErrTranscodeFailed) {
		t.Fatalf("failed source real manifest admitted: %v", err)
	}
	if _, err := session.BuildPlaybackManifest("", ""); !errors.Is(err, playback.ErrTranscodeFailed) {
		t.Fatalf("failed source synthetic manifest admitted: %v", err)
	}
}

func TestNativeInputCopySegmentRecoveryReopensSource(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	target, ok, err := session.ResolveSegmentRecoveryTarget(context.Background(), 1)
	if err != nil || !ok {
		t.Fatalf("copy recovery: %v %v", ok, err)
	}
	if target.SeekSeconds < 0.99 || target.SeekSeconds > 1.01 || target.StartSegmentNumber != 1 {
		t.Fatalf("wrong recovery: %+v", target)
	}
	if opens.Load() != 2 || released.Load() != 2 {
		t.Fatal("copy recovery did not join a fresh source probe")
	}
}

func TestNativeInputStickyReadFailureInvalidatesOutput(t *testing.T) {
	opts, _, released := nativeInputFixture(t, 1)
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if !errors.Is(session.WaitError(), nativestorage.ErrToolInputBudget) {
		t.Fatalf("lost source failure: %v", session.WaitError())
	}
	if released.Load() != 1 {
		t.Fatal("failed input was not joined before Done")
	}
}

func TestNativeInputRequestDisconnectAndExplicitCancel(t *testing.T) {
	opts, _, released := nativeInputFixture(t)
	// A trusted wrapper paces ffmpeg so cancellation exercises an active child.
	marker := filepath.Join(t.TempDir(), "child-environment")
	wrapper := filepath.Join(t.TempDir(), "paced-ffmpeg")
	script := "#!/bin/sh\nprintf '%s' \"${HTTP_PROXY-}${http_proxy-}${HTTPS_PROXY-}${ALL_PROXY-}${NO_PROXY-}${FFREPORT-}\" > '" + marker + "'\nexec '" + opts.FFmpegPath + "' -re \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FFREPORT"} {
		t.Setenv(key, "synthetic-forbidden-value")
	}
	opts.FFmpegPath = wrapper
	requestCtx, disconnect := context.WithCancel(context.Background())
	session, err := playback.StartTranscode(requestCtx, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(marker); err == nil {
			if len(data) != 0 {
				t.Fatal("child inherited proxy/report variables")
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("child did not start")
		case <-tick.C:
		}
	}
	disconnect()
	if !session.IsRunning() || released.Load() != 0 {
		t.Fatal("HTTP disconnect closed execution input")
	}
	if err := session.CloseProcess(); err != nil {
		t.Fatal(err)
	}
	if session.IsRunning() || released.Load() != 1 {
		t.Fatal("cancel returned before source cleanup")
	}
}
