package playback_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type prepareInputHost struct {
	path    string
	binding playback.TranscodeInputBinding
	inputs  int
}

func (h *prepareInputHost) Handles(path string) bool { return path == h.path }
func (h *prepareInputHost) ServeDirect(w http.ResponseWriter, _ *http.Request, _ string) error {
	w.WriteHeader(http.StatusNotFound)
	return nil
}
func (h *prepareInputHost) Input(context.Context, string) (playback.TranscodeInputBinding, error) {
	h.inputs++
	return h.binding, nil
}

func assertPreparedMP4(t *testing.T, output string) {
	t.Helper()
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 || string(data[4:8]) != "ftyp" {
		t.Fatalf("prepared file is not an MP4: %d bytes", len(data))
	}
	if _, err := os.Stat(output + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

// Offline conversion of a native input: the API's download preparation hands
// PrepareFile a context-scoped input, which is encoded from the host's source
// and released before PrepareFile returns.
func TestPrepareFileEncodesContextScopedInput(t *testing.T) {
	opts, opens, released := nativeInputFixture(t)
	source := opts.InputSource
	opts.InputSource, opts.InputFormat = nil, ""
	opts.SessionID, opts.TargetCodecVideo, opts.TargetCodecAudio, opts.SubtitleTrackIndex = "download-prepare-job", "h264", "aac", -1
	host := &prepareInputHost{path: opts.InputPath, binding: playback.TranscodeInputBinding{InputPath: opts.InputPath, InputFormat: "mov", SessionID: opts.SessionID, InputSource: source}}
	output := filepath.Join(t.TempDir(), "prepared.mp4")
	if err := playback.PrepareFile(playback.WithContextMediaHost(context.Background(), host), opts, output); err != nil {
		t.Fatal(err)
	}
	assertPreparedMP4(t, output)
	if host.inputs != 1 || opens.Load() != 1 || released.Load() != 1 {
		t.Fatalf("inputs=%d opens=%d released=%d", host.inputs, opens.Load(), released.Load())
	}
}

// A worker prepares from the source its grant bound to the request, and only
// for the preparation the grant names.
func TestPrepareFileUsesWorkerBoundSource(t *testing.T) {
	opts, opens, _ := nativeInputFixture(t)
	source := opts.InputSource
	opts.InputSource, opts.InputFormat = nil, ""
	opts.SessionID, opts.SubtitleTrackIndex = "download-prepare-job", -1
	output := filepath.Join(t.TempDir(), "prepared.mp4")
	other := playback.WithTranscodeInputSource(context.Background(), playback.TranscodeInputBinding{InputPath: opts.InputPath, InputFormat: "mov", SessionID: "download-prepare-other", InputSource: source})
	if err := playback.PrepareFile(other, opts, output); err == nil || opens.Load() != 0 {
		t.Fatalf("another preparation's source was used: err=%v opens=%d", err, opens.Load())
	}
	bound := playback.WithTranscodeInputSource(context.Background(), playback.TranscodeInputBinding{InputPath: opts.InputPath, InputFormat: "mov", SessionID: opts.SessionID, InputSource: source})
	if err := playback.PrepareFile(bound, opts, output); err != nil {
		t.Fatal(err)
	}
	assertPreparedMP4(t, output)
}

// A native catalog path is a reference: without a source it is refused, and
// ffmpeg never sees it.
func TestPrepareFileRefusesUnboundNativeInput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "prepared.mp4")
	for _, path := range []string{"bloem-storage:location/entry", "/media/movies/Remote.strm"} {
		opts := playback.TranscodeOpts{InputPath: path, FFmpegPath: "/nonexistent/ffmpeg", TargetCodecVideo: "h264", TargetCodecAudio: "aac", SubtitleTrackIndex: -1}
		if err := playback.PrepareFile(context.Background(), opts, output); err == nil {
			t.Fatalf("%s prepared without a source", path)
		}
	}
	if _, err := os.Stat(filepath.Dir(output)); err != nil {
		t.Fatal(err)
	}
}

type prepareProgressReadings struct {
	mu       sync.Mutex
	readings []playback.PrepareProgress
}

func (r *prepareProgressReadings) PrepareProgress(p playback.PrepareProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readings = append(r.readings, p)
}

// A native source's prepared download reads its progress from the frames
// encoded, as Silo's path does, so a subtitle with no cue yet cannot hold it
// back; a copied video keeps FFmpeg's out_time.
func TestPrepareFileNativeInputReadsFramesWhenEncodingVideo(t *testing.T) {
	opts, _, _ := nativeInputFixture(t)
	source := opts.InputSource
	opts.InputSource, opts.InputFormat = nil, ""
	opts.SessionID, opts.SubtitleTrackIndex = "download-prepare-job", -1
	dir := t.TempDir()
	script := filepath.Join(dir, "ffmpeg")
	// 1500 frames at 24 fps is 62.5 s, further than FFmpeg's 30 s.
	body := "#!/bin/sh\n" +
		"printf 'frame=1500\\nfps=240\\nout_time_us=30000000\\nspeed=4.0x\\nprogress=continue\\n'\n" +
		"eval \"touch \\${$#}\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		videoCodec string
		want       float64
	}{
		{"h264", 62.5},
		{"copy", 30},
	} {
		progress := &prepareProgressReadings{}
		run := opts
		run.FFmpegPath, run.TargetCodecVideo, run.TargetCodecAudio = script, tc.videoCodec, "aac"
		run.TotalDuration, run.SourceFrameRate, run.PrepareProgressSink = 120, 24, progress
		bound := playback.WithTranscodeInputSource(context.Background(), playback.TranscodeInputBinding{InputPath: run.InputPath, InputFormat: "mov", SessionID: run.SessionID, InputSource: source})
		if err := playback.PrepareFile(bound, run, filepath.Join(dir, tc.videoCodec+".mp4")); err != nil {
			t.Fatalf("%s: PrepareFile: %v", tc.videoCodec, err)
		}
		progress.mu.Lock()
		got := progress.readings
		progress.mu.Unlock()
		if len(got) != 1 || got[0].EncodedSeconds != tc.want {
			t.Fatalf("%s: readings = %+v, want %vs", tc.videoCodec, got, tc.want)
		}
	}
}
