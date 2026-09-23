package playback

// Bloem strict reconstruct admission coverage (playback.strict_reconstruct_admission).
// Silo's transcode_manager_test.go keeps the default fail-open cases.

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// With playback.strict_reconstruct_admission enabled, the same unevaluated-limit
// case must refuse instead. This is Bloem's deliberate divergence from upstream,
// expressed as an operator setting rather than a fork of the admission path.
func TestReconstructSession_ProviderErrorFailsClosedUnderStrictAdmission(t *testing.T) {
	ctx := context.Background()
	reg := &fakeSessionRegistry{
		limitsErr: fmt.Errorf("load session limits for user 7: %w",
			errors.Join(ErrLimitProviderUnavailable, errors.New("db timeout"))),
	}
	m := NewTranscodeManager()
	m.Sessions = reg
	m.StrictAdmissionFn = func() bool { return true }

	card := NewDirectRecipeCard("a", 7, "p", 100)
	got := m.ReconstructSession(ctx, "a", 7, card)
	if got != nil {
		t.Fatalf("strict admission must refuse an unevaluated limit provider, got session %+v", got)
	}
	if _, err := reg.GetSession("a"); err == nil {
		t.Fatal("refused session was unexpectedly registered")
	}
}

// ...and must yield SessionMissing under the strict posture.
func TestLoadOrReconstructSession_ProviderErrorFailsClosedUnderStrictAdmission(t *testing.T) {
	ctx := context.Background()
	reg := &fakeSessionRegistry{
		limitsErr: errors.Join(ErrLimitProviderUnavailable, errors.New("db timeout")),
	}
	m := NewTranscodeManager()
	m.Sessions = reg
	m.StrictAdmissionFn = func() bool { return true }

	card := NewDirectRecipeCard("s", 5, "p", 77)
	got, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 5, &card)
	if status != SessionMissing || got != nil {
		t.Fatalf("strict admission must yield SessionMissing, got status=%v session=%+v", status, got)
	}
}
