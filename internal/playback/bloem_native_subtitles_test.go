package playback_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
)

func nativeSubtitleFixture(t *testing.T) playback.TranscodeOpts {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(sub, []byte("1\n00:00:00,100 --> 00:00:00,900\nNative captions\n\n2\n00:00:01,500 --> 00:00:02,000\nWindow caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "video.mkv")
	cmd := exec.Command(bin, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:s=128x96:r=24", "-i", sub, "-t", "4", "-c:v", "libx264", "-threads", "1", "-g", "24", "-pix_fmt", "yuv420p", "-c:s", "srt", path)
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if _, err := os.Stat(font); err == nil {
		cmd.Args = append(cmd.Args[:len(cmd.Args)-1], "-attach", font, "-metadata:s:t", "mimetype=application/x-truetype-font", path)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subtitle fixture: %v %s", err, out)
	}
	opts := playback.TranscodeOpts{InputPath: "native-storage:subtitle-pinned-ref", InputFormat: "matroska", FFmpegPath: bin, OutputDir: t.TempDir(), HWAccel: "none", TargetCodecVideo: "h264", TargetCodecAudio: "copy", SegmentDuration: 1, TotalDuration: 4, SubtitleTrackIndex: 0, SubtitleCodec: "subrip", SubtitleBurnIn: true}
	opts.InputSource = &playback.TranscodeInputSource{Open: func(ctx context.Context, _ playback.TranscodeOpts) (playback.TranscodeInput, error) {
		ctx, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(ctx, path)
		if err != nil {
			cancel()
			return nil, err
		}
		input, err := nativestorage.NewToolInput(ctx, file, nativestorage.ToolInputConfig{CancelSource: cancel, Reauthorize: func(context.Context) error { return nil }, MaxBytes: 16 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 30 * time.Second})
		if err != nil {
			cancel()
			_ = file.Close()
			return nil, err
		}
		return input, nil
	}}
	return opts
}

func TestNativeSubtitleWindowKeepsSourceTimeline(t *testing.T) {
	opts := nativeSubtitleFixture(t)
	data, err := playback.ExtractSourceSubtitle(t.Context(), opts, 0, "vtt", 1, 1.2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Window caption") || !strings.Contains(string(data), "00:01.500") {
		t.Fatalf("window rebased source timestamps: %s", data)
	}
}

func TestNativeSubtitleFontsUseBoundedFreshSource(t *testing.T) {
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	expected, err := os.ReadFile(font)
	if err != nil {
		t.Skip("font fixture unavailable")
	}
	opts := nativeSubtitleFixture(t)
	fonts, err := playback.ExtractSourceSubtitleFonts(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(fonts) != 1 || string(fonts[0].Data) != string(expected) {
		t.Fatal("attached font bytes changed")
	}
}

func TestNativeSubtitleExtractsTextFromFreshSource(t *testing.T) {
	opts := nativeSubtitleFixture(t)
	data, err := playback.ExtractSourceSubtitle(context.Background(), opts, 0, "vtt", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "WEBVTT") || !strings.Contains(string(data), "Native captions") {
		t.Fatalf("subtitle bytes: %q", data)
	}
	if _, err := playback.ExtractSourceSubtitle(context.Background(), opts, 0, "hls", 0, 0); err == nil {
		t.Fatal("unreviewed subtitle muxer accepted")
	}
}

func TestNativeSubtitleBurnInAndRestartRetainCatalogIdentity(t *testing.T) {
	opts := nativeSubtitleFixture(t)
	session, err := playback.StartTranscode(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
	if session.Opts().InputPath != opts.InputPath || session.Opts().SubtitleTrackIndex != 0 {
		t.Fatal("durable identity changed")
	}
	if err := session.Restart(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	waitNativeInput(t, session)
	if err := session.WaitError(); err != nil {
		t.Fatal(err)
	}
}
