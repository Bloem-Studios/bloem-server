package playback

// Bloem-owned test. Kept out of Silo's own test files so upstream merges do not conflict here;
// see contracts/seams.txt.

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// nativeRetryPrivateArgument stands in for the private address a native
// input hands FFmpeg. The fake FFmpeg names it on stderr, as a real one does
// when it fails to open or decode an input.
const nativeRetryPrivateArgument = "http://10.20.30.40:41234/private-source-token"

// nativeRetryInput is a TranscodeInput whose Run hands its consumer
// nativeRetryPrivateArgument under the execution lifetime it was opened with.
// It serves no bytes: the fake FFmpeg never reads.
type nativeRetryInput func(consume func(context.Context, string) error) error

func (run nativeRetryInput) Run(consume func(context.Context, string) error) error {
	return run(consume)
}

func (nativeRetryInput) Close() error { return nil }

func nativeRetrySource() *TranscodeInputSource {
	return &TranscodeInputSource{Open: func(ctx context.Context, _ TranscodeOpts) (TranscodeInput, error) {
		return nativeRetryInput(func(consume func(context.Context, string) error) error {
			return consume(ctx, nativeRetryPrivateArgument)
		}), nil
	}}
}

// nativeRetryLogSink records every persisted FFmpeg log line and event.
type nativeRetryLogSink struct {
	mu   sync.Mutex
	text strings.Builder
}

func (s *nativeRetryLogSink) WriteLine(_ context.Context, _ string, _ FFmpegLogAttrs, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text.WriteString(line + "\n")
}

func (s *nativeRetryLogSink) WriteEvent(_ context.Context, _ string, _ FFmpegLogAttrs, message string) {
	s.WriteLine(context.Background(), "", FFmpegLogAttrs{}, message)
}

func (s *nativeRetryLogSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text.String()
}

type nativeRetryLockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *nativeRetryLockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *nativeRetryLockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureNativeRetryLogs sends the log and slog defaults to one buffer for
// the rest of the test.
func captureNativeRetryLogs(t *testing.T) *nativeRetryLockedBuffer {
	t.Helper()
	logs := &nativeRetryLockedBuffer{}
	previousLog, flags := log.Writer(), log.Flags()
	previousSlog := slog.Default()
	log.SetOutput(logs)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		log.SetOutput(previousLog)
		log.SetFlags(flags)
		slog.SetDefault(previousSlog)
	})
	return logs
}

// writeNativeRetryFFmpeg writes a fake FFmpeg that logs each invocation, fails
// a CUDA-decoded attempt with NVDEC's rejection after naming its input, runs
// mixed for an NVENC attempt that decodes on the CPU, and runs success for any
// other attempt. Both scripts see $last, the final argument.
func writeNativeRetryFFmpeg(t *testing.T, dir, mixed, success string) (script, invocations string) {
	t.Helper()
	invocations = filepath.Join(dir, "invocations.log")
	script = filepath.Join(dir, "ffmpeg")
	fake := "#!/bin/sh\n" +
		"for last; do :; done\n" +
		"printf '%s\\n' \"$*\" >> '" + invocations + "'\n" +
		"input=''; previous=''\n" +
		"for arg; do if [ \"$previous\" = -i ]; then input=\"$arg\"; fi; previous=\"$arg\"; done\n" +
		"case \"$*\" in\n" +
		"  *'-hwaccel cuda'*) echo \"[in#0 @ 0x1] Opening '$input' for reading\" >&2\n" +
		"    echo '[hevc @ 0x1] Hardware is lacking required capabilities' >&2; exit 1 ;;\n" +
		"  *h264_nvenc*) " + mixed + " ;;\n" +
		"  *) " + success + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(fake), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return script, invocations
}

func readNativeRetryInvocations(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invocation log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func assertNoNativeRetryPrivateArgument(t *testing.T, where, text string) {
	t.Helper()
	if strings.Contains(text, nativeRetryPrivateArgument) || strings.Contains(text, "10.20.30.40") {
		t.Fatalf("%s carries the private input address: %q", where, text)
	}
}

// Offline conversion of a native input under explicit NVENC whose NVDEC
// rejects the source decodes on the CPU with NVENC, as Silo's PrepareFile
// does, and moves on to software only when that attempt wrote no output. The
// rejection names the private input address, which never reaches the error
// or a log.
func TestPrepareFileNativeInputRetriesRejectedDecodeOnCPU(t *testing.T) {
	for _, tt := range []struct {
		name         string
		mixed        string
		wantErr      bool
		wantAttempts int
	}{
		{name: "mixed attempt succeeds", mixed: "printf x > \"$last\"; exit 0", wantAttempts: 2},
		{name: "mixed attempt dies while starting", mixed: "exit 1", wantAttempts: 3},
		{name: "mixed attempt fails after writing output", mixed: "printf partial > \"$last\"; exit 1", wantErr: true, wantAttempts: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetDeviceLoad(t)
			logs := captureNativeRetryLogs(t)
			dir := t.TempDir()
			script, invocations := writeNativeRetryFFmpeg(t, dir, tt.mixed, "printf x > \"$last\"; exit 0")
			sink := &nativeRetryLogSink{}
			output := filepath.Join(dir, "artifact.mp4")

			err := PrepareFile(context.Background(), TranscodeOpts{
				InputPath:          "native-storage:pinned-ref",
				InputSource:        nativeRetrySource(),
				InputFormat:        "mov",
				SessionID:          "download-prepare-job",
				FFmpegPath:         script,
				FFmpegLogSink:      sink,
				SourceVideoCodec:   "hevc",
				TargetCodecVideo:   "h264",
				TargetCodecAudio:   "aac",
				TargetResolution:   "720p",
				TargetBitrateKbps:  2000,
				HWAccel:            transcodeHWNVENC,
				AudioTrackIndex:    -1,
				SubtitleTrackIndex: -1,
			}, output)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PrepareFile() error = %v, want error %v", err, tt.wantErr)
			}
			attempts := readNativeRetryInvocations(t, invocations)
			if len(attempts) != tt.wantAttempts || !strings.Contains(attempts[0], "-hwaccel cuda") ||
				!strings.Contains(attempts[1], "h264_nvenc") || strings.Contains(attempts[1], "-hwaccel cuda") {
				t.Fatalf("attempts = %q, want CUDA decode, then CPU decode with NVENC", attempts)
			}
			if tt.wantAttempts == 3 && !strings.Contains(attempts[2], "libx264") {
				t.Fatalf("third attempt = %q, want libx264", attempts[2])
			}
			if !tt.wantErr {
				if _, statErr := os.Stat(output); statErr != nil {
					t.Fatalf("artifact missing: %v", statErr)
				}
			}
			if hwDeviceActiveCount(DefaultNVENCDevice) != 0 {
				t.Fatalf("NVENC workloads after PrepareFile = %d, want 0", hwDeviceActiveCount(DefaultNVENCDevice))
			}
			if err != nil {
				assertNoNativeRetryPrivateArgument(t, "error", err.Error())
			}
			assertNoNativeRetryPrivateArgument(t, "logs", logs.String())
			assertNoNativeRetryPrivateArgument(t, "FFmpeg log sink", sink.String())
		})
	}
}

// A live start of a native input under explicit NVENC whose NVDEC rejects
// the source restarts with CPU decoding and the NVENC encoder, as Silo's
// startup retry does for a file. Without stderr the rejection was never
// recognized and the start failed. The rejection names the private input
// address, which never reaches the error or a log.
func TestStartReadyTranscodeNativeInputRetriesDecoderRejectionOnCPU(t *testing.T) {
	resetDeviceLoad(t)
	logs := captureNativeRetryLogs(t)
	dir := t.TempDir()
	manifest := "#EXTM3U\\n#EXT-X-TARGETDURATION:2\\n" +
		"#EXTINF:2,\\nseg_00000.ts\\n#EXTINF:2,\\nseg_00001.ts\\n#EXTINF:2,\\nseg_00002.ts\\n"
	ready := "for n in 0 1 2; do printf x > seg_0000$n.ts; done; printf '" + manifest + "' > stream.m3u8; exec sleep 30"
	script, invocations := writeNativeRetryFFmpeg(t, dir, ready, ready)
	sink := &nativeRetryLogSink{}

	opts := TranscodeOpts{
		InputPath:          "native-storage:pinned-ref",
		InputSource:        nativeRetrySource(),
		InputFormat:        "mov",
		SessionID:          "playback-native-1",
		OutputDir:          filepath.Join(dir, "out"),
		FFmpegPath:         script,
		FFmpegLogSink:      sink,
		SourceVideoCodec:   "hevc",
		TargetCodecVideo:   "h264",
		TargetCodecAudio:   "aac",
		TargetResolution:   "720p",
		HWAccel:            transcodeHWNVENC,
		SegmentDuration:    2,
		AudioTrackIndex:    -1,
		SubtitleTrackIndex: -1,
	}
	pipeline := newAutoTranscodePipeline(context.Background(), opts, newAutoTranscodePipelineCache())
	session, err := StartReadyTranscode(context.Background(), pipeline, TranscodeStartup{
		Timeout:     10 * time.Second,
		LegacyRetry: TranscodeStartupRetryAccelChange,
	})
	if session != nil {
		t.Cleanup(func() { _ = session.Close() })
	}
	if err != nil {
		assertNoNativeRetryPrivateArgument(t, "error", err.Error())
		t.Fatalf("StartReadyTranscode() error = %v, want a start on CPU decoding", err)
	}
	attempts := readNativeRetryInvocations(t, invocations)
	if len(attempts) != 2 || !strings.Contains(attempts[0], "-hwaccel cuda") ||
		!strings.Contains(attempts[1], "h264_nvenc") || strings.Contains(attempts[1], "-hwaccel cuda") {
		t.Fatalf("attempts = %q, want CUDA decode, then CPU decode with NVENC", attempts)
	}
	if got := session.Opts(); !got.SoftwareVideoDecode || got.HWAccel != transcodeHWNVENC {
		t.Fatalf("started session HWAccel=%q SoftwareVideoDecode=%v, want nvenc with CPU decoding", got.HWAccel, got.SoftwareVideoDecode)
	}
	assertNoNativeRetryPrivateArgument(t, "logs", logs.String())
	assertNoNativeRetryPrivateArgument(t, "FFmpeg log sink", sink.String())
}
