package playback_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/mediasource"
	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/Silo-Server/silo-server/internal/playback"
)

type stalledFontTail struct {
	mediasource.File
	ctx    context.Context
	prefix int64
}

func (f *stalledFontTail) ReadAt(p []byte, offset int64) (int, error) {
	if offset >= f.prefix {
		<-f.ctx.Done()
		return 0, f.ctx.Err()
	}
	return f.File.ReadAt(p, offset)
}

// The metadata open remains readable. The fresh extraction open has enough
// media for initial probing but cannot read the tail before cancellation.
func TestNativeSubtitleFontsDoNotWaitForMediaTail(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	want := []byte("native-attached-font-bytes")
	font, media := filepath.Join(dir, "font.ttf"), filepath.Join(dir, "media.mkv")
	if err := os.WriteFile(font, want, 0600); err != nil {
		t.Fatal(err)
	}
	fixtureCtx, fixtureCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer fixtureCancel()
	cmd := exec.CommandContext(fixtureCtx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=640x360:r=10:d=60",
		"-c:v", "ffv1", "-threads", "1", "-attach", font, "-metadata:s:t", "mimetype=application/x-truetype-font", media)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("media fixture: %v %s", err, out)
	}
	var opens atomic.Int32
	opts := playback.TranscodeOpts{InputPath: "native-storage:font-tail", InputFormat: "matroska", FFmpegPath: ffmpeg}
	opts.InputSource = &playback.TranscodeInputSource{Open: func(ctx context.Context, _ playback.TranscodeOpts) (playback.TranscodeInput, error) {
		ctx, cancel := context.WithCancel(ctx)
		file, err := mediasource.OpenLocal(ctx, media)
		if err != nil {
			cancel()
			return nil, err
		}
		if opens.Add(1) == 2 {
			file = &stalledFontTail{File: file, ctx: ctx, prefix: file.Info().Size / 2}
		}
		input, err := nativestorage.NewToolInput(ctx, file, nativestorage.ToolInputConfig{CancelSource: cancel,
			Reauthorize: func(context.Context) error { return nil }, MaxBytes: 128 << 20, ChunkBytes: 4096, MaxConcurrentReads: 2, Timeout: 10 * time.Second})
		if err != nil {
			cancel()
			_ = file.Close()
			return nil, err
		}
		return input, nil
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	fonts, err := playback.ExtractSourceSubtitleFonts(ctx, opts)
	if err != nil {
		t.Fatalf("font extraction waited for unavailable media tail: %v", err)
	}
	if len(fonts) != 1 || !bytes.Equal(fonts[0].Data, want) {
		t.Fatal("attached font bytes changed")
	}
}
