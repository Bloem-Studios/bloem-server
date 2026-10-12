package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeAudioProbeFormatsAndFractionalDuration(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal("finite audio fixtures require ffmpeg")
	}
	binary := nativeProbeBinary(t)
	for _, fixture := range []struct{ ext, codec string }{
		{".mp3", "libmp3lame"}, {".flac", "flac"}, {".ogg", "libvorbis"}, {".opus", "libopus"},
		{".wav", "pcm_s16le"}, {".m4b", "aac"},
	} {
		t.Run(fixture.ext, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "track"+fixture.ext)
			command := exec.CommandContext(t.Context(), ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=32000", "-t", "2.75", "-c:a", fixture.codec, "-metadata", "title=Finite audio", output)
			if result, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generate audio: %v: %s", err, result)
			}
			data, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			source, file, ctx, cfg := nativeProbeFixture(t, data)
			demuxer, ok := NativeAudioDemuxer(output)
			if !ok {
				t.Fatal("finite audio format not admitted")
			}
			cfg.Demuxer = demuxer
			got, err := ProbeNativeAudio(ctx, binary, file, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.Probe.CodecAudio == "" || len(got.Probe.AudioTracks) != 1 || got.Tags["title"] != "Finite audio" {
				t.Fatalf("lost owning parser facts: %+v", got)
			}
			if got.DurationSeconds < 2.74 || got.DurationSeconds > 2.9 || got.DurationSeconds == math.Trunc(got.DurationSeconds) {
				t.Fatalf("fractional duration lost: %v", got.DurationSeconds)
			}
			if got.Probe.Duration != 2 {
				t.Fatalf("shared duration changed: %d", got.Probe.Duration)
			}
			fetched, _ := source.fetched()
			if fetched == 0 || fetched > cfg.Input.MaxBytes || file.closed.Load() != 1 {
				t.Fatalf("pinned input lifecycle: fetched %d closed %d", fetched, file.closed.Load())
			}
		})
	}
}

func TestNativeAudioProbeRetainsFractionalTerminalChapter(t *testing.T) {
	const response = `{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"2.75","tags":{"title":"Book"}},"streams":[{"index":0,"codec_type":"audio","codec_name":"aac","channels":1}],"chapters":[{"id":0,"start_time":"0","end_time":"1.25","tags":{"title":"One"}},{"id":1,"start_time":"1.25","end_time":"2.75","tags":{"title":"Two"}}]}`
	got, err := runNativeAudioProbeJSON(t, response)
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationSeconds != 2.75 || len(got.Chapters) != 2 || got.Chapters[1].StartSeconds != 1.25 || got.Chapters[1].EndSeconds != 2.75 {
		t.Fatalf("terminal chapter truncated: %+v", got)
	}
	if got.Probe.Duration != 2 {
		t.Fatalf("shared duration changed: %+v", got.Probe)
	}
}

func TestNativeAudioProbeRejectsMalformedTimingAndVideo(t *testing.T) {
	for _, response := range []string{
		`{"format":{"format_name":"mp3","duration":"NaN"},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`,
		`{"format":{"format_name":"mp3","duration":"-1"},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`,
		`{"format":{"format_name":"mp3","duration":"1000001"},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`,
		`{"format":{"format_name":"mp3","duration":"2.75"},"streams":[{"codec_type":"video","codec_name":"h264"}]}`,
		`{"format":{"format_name":"mp3","duration":"2.75"},"streams":[{"codec_type":"audio","codec_name":"mp3"}],"chapters":[{"start_time":"-1","end_time":"1"}]}`,
		`{"format":{"format_name":"mp3","duration":"2.75"},"streams":[{"codec_type":"audio","codec_name":"mp3"}],"chapters":[{"start_time":"0","end_time":"NaN"}]}`,
		`{"format":{"format_name":"mp3","duration":"2.75"},"streams":[{"codec_type":"audio","codec_name":"mp3"}],"chapters":[{"start_time":"0","end_time":"3"}]}`,
	} {
		got, err := runNativeAudioProbeJSON(t, response)
		if got != nil || !errors.Is(err, ErrNativeProbeInvalid) {
			t.Fatalf("invalid media accepted: %+v %v", got, err)
		}
	}
	for _, name := range []string{"track.aac", "shortcut.strm", "manifest.m3u8", "movie.mkv"} {
		if _, ok := NativeAudioDemuxer(name); ok {
			t.Fatalf("unreviewed audio admitted: %s", name)
		}
	}
}

func runNativeAudioProbeJSON(t *testing.T, response string) (*NativeAudioProbe, error) {
	t.Helper()
	if !json.Valid([]byte(response)) {
		t.Fatal("bad test fixture")
	}
	tool := filepath.Join(t.TempDir(), "ffprobe-fixture")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ncat <<'NATIVE_AUDIO_PROBE'\n"+response+"\nNATIVE_AUDIO_PROBE\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, file, ctx, cfg := nativeProbeFixture(t, []byte("finite pinned bytes"))
	cfg.Input.Reauthorize = func(context.Context) error { return nil }
	return ProbeNativeAudio(ctx, tool, file, cfg)
}
