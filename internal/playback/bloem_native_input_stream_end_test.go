package playback

import (
	"context"
	"testing"
	"time"
)

// scriptInput runs the session's FFmpeg script on its own argument, as a
// native source input does, and holds nothing else open.
type scriptInput struct{}

func (scriptInput) Run(consume func(context.Context, string) error) error {
	return consume(context.Background(), "pipe:0")
}

func (scriptInput) Close() error { return nil }

// A run that reads a source input has no Silo stderr writer. Its FFmpeg can
// still exit 0 after a failed read, and that run must not teach the session
// where the file ends, exactly as a local file's run would not.
func TestNativeInputDemuxErrorLearnsNoStreamEnd(t *testing.T) {
	const demuxError = "[in#0/matroska,webm @ 0x1] Error during demuxing: Input/output error"
	for _, tt := range []struct {
		name    string
		stderr  string
		wantEnd bool
	}{
		{"clean exit learns the end", "", true},
		{"exit 0 after a demux error learns nothing", demuxError, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeFFmpegSession(t, "h264", true, tt.stderr, 0)
			s.opts.InputSource = &TranscodeInputSource{Open: func(context.Context, TranscodeOpts) (TranscodeInput, error) {
				return scriptInput{}, nil
			}}
			s.opts.InputFormat = "matroska"
			runFakeFFmpeg(t, s, 72)
			if _, known := s.LearnedStreamEnd(); known != tt.wantEnd {
				t.Fatalf("stream end known = %v, want %v", known, tt.wantEnd)
			}
			if tt.wantEnd {
				return
			}
			decision := s.SegmentRecoveryDecision(75, time.Now())
			if decision.Reason == "past_end_of_stream" {
				t.Fatalf("decision = %+v, want a restart, not the end of the stream", decision)
			}
		})
	}
}
