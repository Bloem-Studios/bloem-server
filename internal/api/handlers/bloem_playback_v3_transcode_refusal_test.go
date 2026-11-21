package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type transcodeRefusingSessions struct {
	SessionManagerInterface
	err error
}

func (s transcodeRefusingSessions) CheckTranscodingAllowed(context.Context, int, string, bool) error {
	return s.err
}

// Bloem checks the profile's transcoding permission before the v3 start
// creates its session. A Silo app reads the terminal that refusal produces, so
// it must be the one Silo's session start answers for the same error.
func TestBloemStartTranscodingRefusalIsSilosTerminal(t *testing.T) {
	for _, err := range []error{
		playback.ErrTranscodingDisabled,
		playback.ErrAudioTranscodingDisabled,
		errors.New("policy read failed"),
	} {
		h := &PlaybackHandler{sessionMgr: transcodeRefusingSessions{err: err}}
		got := h.bloemCheckStartTranscodingAllowedV3(context.Background(), 1, "p1", playback.PlayTranscode, false)
		want := sessionStartErrorV3(err)
		if got == nil {
			t.Fatalf("%v: not refused, want Silo's %q", err, want.reason)
		}
		if got.reason != want.reason || got.message != want.message || got.retryable != want.retryable {
			t.Fatalf("%v: refusal = %q %q retryable=%v, want Silo's %q %q retryable=%v",
				err, got.reason, got.message, got.retryable, want.reason, want.message, want.retryable)
		}
	}
	allowed := &PlaybackHandler{sessionMgr: transcodeRefusingSessions{}}
	if got := allowed.bloemCheckStartTranscodingAllowedV3(context.Background(), 1, "p1", playback.PlayTranscode, false); got != nil {
		t.Fatalf("allowed transcode refused: %+v", got)
	}
}
