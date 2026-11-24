package playback_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

// Stream insights records every refusal at a stream or transcode limit. A
// fresh start is refused inside StartSessionWithFilesContext, which defers
// noteStreamLimit with its error; these tests pin that a transcode refusal
// comes back through that call on both admission paths.
func TestTranscodeLimitRefusalPassesTheDeferredHook(t *testing.T) {
	t.Run("inline admission", func(t *testing.T) {
		mgr := playback.NewSessionManager(0, 0)
		mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
			return playback.SessionLimits{MaxTranscodes: 1}, nil
		})
		if _, err := mgr.StartSessionWithFilesContext(context.Background(), 1, "household", 100, 100, playback.PlayTranscode, false); err != nil {
			t.Fatalf("first transcode: %v", err)
		}
		_, err := mgr.StartSessionWithFilesContext(context.Background(), 1, "household", 101, 101, playback.PlayTranscode, false)
		if !errors.Is(err, playback.ErrTooManyTranscodes) {
			t.Fatalf("second transcode error = %v, want ErrTooManyTranscodes", err)
		}
	})
	t.Run("policy admission", func(t *testing.T) {
		mgr := playback.NewSessionManager(0, 0)
		mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
			return playback.SessionLimits{}, nil
		})
		mgr.SetAdmissionDecider(func(context.Context, playback.AdmissionRequest) (playback.AdmissionDecision, error) {
			return playback.AdmissionDecision{Allowed: false, ReasonCode: playback.AdmissionReasonMaxTranscodesExceeded}, nil
		})
		_, err := mgr.StartSessionWithFilesContext(context.Background(), 1, "household", 100, 100, playback.PlayTranscode, false)
		if !errors.Is(err, playback.ErrTooManyTranscodes) {
			t.Fatalf("error = %v, want ErrTooManyTranscodes", err)
		}
	})
}

// A mid-stream switch that needs a transcode is refused by
// CheckReplacementAllowed, outside the deferred hook. Production always
// installs an admission decider, and this pins that the decider makes that
// refusal, so a Bloem wrapper around the decider hears it.
func TestReplacementTranscodeRefusalGoesThroughTheAdmissionDecider(t *testing.T) {
	var mu sync.Mutex
	var seen []playback.AdmissionRequest
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxTranscodes: 1}, nil
	})
	mgr.SetAdmissionDecider(func(_ context.Context, req playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		mu.Lock()
		seen = append(seen, req)
		mu.Unlock()
		if req.RequiresVideoTranscode {
			return playback.AdmissionDecision{Allowed: false, ReasonCode: playback.AdmissionReasonMaxTranscodesExceeded}, nil
		}
		return playback.AdmissionDecision{Allowed: true}, nil
	})
	session, err := mgr.StartSessionWithFilesContext(context.Background(), 1, "household", 100, 100, playback.PlayDirect, false)
	if err != nil {
		t.Fatalf("direct start: %v", err)
	}
	err = mgr.CheckReplacementAllowed(context.Background(), session.ID, playback.PlayTranscode, false)
	if !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("switch to transcode error = %v, want ErrTooManyTranscodes", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("decider calls = %d, want 2 (start, switch)", len(seen))
	}
	if last := seen[1]; last.UserID != 1 || !last.RequiresVideoTranscode || last.RequestedMethod != playback.PlayTranscode {
		t.Fatalf("switch request = %+v, want user 1 asking for a video transcode", last)
	}
}
