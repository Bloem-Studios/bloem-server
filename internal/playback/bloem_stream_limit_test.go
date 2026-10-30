package playback_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type streamLimitRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *streamLimitRecorder) StreamLimitReached(_ context.Context, userID int, profileID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, profileID)
	_ = userID
}

func TestStreamLimitRefusalTellsTheObserver(t *testing.T) {
	recorder := &streamLimitRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxStreams: 1}, nil
	})
	mgr.SetStreamLimitObserver(recorder)

	if _, err := mgr.StartSession(1, "household", 100, playback.PlayDirect, false); err != nil {
		t.Fatal(err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("an admitted stream told the observer: %v", recorder.calls)
	}
	if _, err := mgr.StartSession(1, "household", 101, playback.PlayDirect, false); !errors.Is(err, playback.ErrTooManyStreams) {
		t.Fatalf("second stream error = %v, want ErrTooManyStreams", err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0] != "household" {
		t.Fatalf("observer calls = %v, want one for the refused profile", recorder.calls)
	}
}

func TestStreamLimitObserverHearsPolicyRefusalsToo(t *testing.T) {
	recorder := &streamLimitRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{}, nil
	})
	mgr.SetAdmissionDecider(func(context.Context, playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		return playback.AdmissionDecision{Allowed: false, ReasonCode: playback.AdmissionReasonMaxStreamsExceeded}, nil
	})
	mgr.SetStreamLimitObserver(recorder)
	if _, err := mgr.StartSession(1, "household", 100, playback.PlayDirect, false); !errors.Is(err, playback.ErrTooManyStreams) {
		t.Fatalf("error = %v, want ErrTooManyStreams", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("observer calls = %v, want 1", recorder.calls)
	}
}

func TestOtherRefusalsDoNotTellTheObserver(t *testing.T) {
	recorder := &streamLimitRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{PlaybackDisabled: true}, nil
	})
	mgr.SetStreamLimitObserver(recorder)
	if _, err := mgr.StartSession(1, "household", 100, playback.PlayDirect, false); err == nil {
		t.Fatal("disabled playback was admitted")
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("observer calls = %v, want none", recorder.calls)
	}
}
