package playback_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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

type refusal struct {
	userID    int
	profileID string
	kind      playback.LimitKind
	scope     playback.LimitScope
}

type refusalRecorder struct {
	mu    sync.Mutex
	heard []refusal
}

func (r *refusalRecorder) LimitRefused(_ context.Context, userID int, profileID string, kind playback.LimitKind, scope playback.LimitScope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heard = append(r.heard, refusal{userID, profileID, kind, scope})
}

func (r *refusalRecorder) all() []refusal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]refusal(nil), r.heard...)
}

func TestLimitRefusalObserverHearsFreshStartRefusals(t *testing.T) {
	refusals := &refusalRecorder{}
	prompts := &streamLimitRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxStreams: 2, MaxTranscodes: 1}, nil
	})
	mgr.SetLimitRefusalObserver(refusals)
	mgr.SetStreamLimitObserver(prompts)
	if _, err := mgr.StartSession(7, "kid", 100, playback.PlayTranscode, false); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartSession(7, "kid", 101, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("second transcode error = %v", err)
	}
	if _, err := mgr.StartSession(7, "kid", 102, playback.PlayDirect, false); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.StartSession(7, "kid", 103, playback.PlayDirect, false); !errors.Is(err, playback.ErrTooManyStreams) {
		t.Fatalf("third stream error = %v", err)
	}
	want := []refusal{{7, "kid", playback.LimitTranscodes, playback.ScopePerson}, {7, "kid", playback.LimitStreams, playback.ScopePerson}}
	if got := refusals.all(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("refusals = %+v, want %+v", got, want)
	}
	if len(prompts.calls) != 1 {
		t.Fatalf("upgrade prompt calls = %v, want one, for the stream refusal only", prompts.calls)
	}
}

// Production's decider is Bloem's policy adapter, which reports each denial
// with NoteAdmissionRefusal. A fresh start is reported once, by the
// deferred hook; a mid-stream switch only the decider refuses is reported
// by the decider, for the account.
func TestDeciderRefusalsAreHeardOnce(t *testing.T) {
	refusals := &refusalRecorder{}
	playback.SetAdmissionRefusalObserver(refusals)
	t.Cleanup(func() { playback.SetAdmissionRefusalObserver(nil) })
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{}, nil
	})
	mgr.SetLimitRefusalObserver(refusals)
	mgr.SetAdmissionDecider(func(ctx context.Context, req playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		decision := playback.AdmissionDecision{Allowed: true}
		if req.RequiresVideoTranscode {
			decision = playback.AdmissionDecision{Allowed: false, ReasonCode: playback.AdmissionReasonMaxTranscodesExceeded}
		}
		if !decision.Allowed {
			playback.NoteAdmissionRefusal(ctx, req.UserID, decision.ReasonCode)
		}
		return decision, nil
	})
	if _, err := mgr.StartSessionWithFilesContext(context.Background(), 7, "kid", 100, 100, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("fresh transcode error = %v", err)
	}
	if got := refusals.all(); len(got) != 1 || got[0] != (refusal{7, "kid", playback.LimitTranscodes, playback.ScopePerson}) {
		t.Fatalf("fresh start refusals = %+v, want one from the deferred hook", got)
	}
	session, err := mgr.StartSessionWithFilesContext(context.Background(), 7, "kid", 101, 101, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.CheckReplacementAllowed(context.Background(), session.ID, playback.PlayTranscode, false); !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("switch error = %v", err)
	}
	got := refusals.all()
	if len(got) != 2 || got[1] != (refusal{7, "", playback.LimitTranscodes, playback.ScopePerson}) {
		t.Fatalf("refusals after the switch = %+v, want the switch heard once, for the account", got)
	}
}

type householdPoolStore struct {
	mu   sync.Mutex
	next int64
}

func (s *householdPoolStore) Acquire(_ context.Context, request playback.ReservationRequest) (playback.Reservation, error) {
	if request.IsTranscode {
		return playback.Reservation{}, playback.ErrHouseholdTranscodesExceeded
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	return playback.Reservation{SessionID: request.SessionID, Generation: s.next, LeaseUntil: request.LeaseUntil}, nil
}

func (s *householdPoolStore) Renew(_ context.Context, sessionID string, generation int64, until time.Time) (playback.Reservation, error) {
	return playback.Reservation{SessionID: sessionID, Generation: generation, LeaseUntil: until}, nil
}

func (s *householdPoolStore) Release(context.Context, string, int64) error { return nil }

// A switch the decider allows can still be refused by the household's
// shared pool, which Bloem's reservation code reports.
func TestSwitchRefusedByTheHouseholdPoolIsHeard(t *testing.T) {
	refusals := &refusalRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{}, nil
	})
	mgr.SetReservationStore(&householdPoolStore{}, time.Minute)
	mgr.SetLimitRefusalObserver(refusals)
	mgr.SetAdmissionDecider(func(context.Context, playback.AdmissionRequest) (playback.AdmissionDecision, error) {
		return playback.AdmissionDecision{Allowed: true}, nil
	})
	session, err := mgr.StartSessionWithFilesContext(context.Background(), 7, "kid", 100, 100, playback.PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.CheckReplacementAllowed(context.Background(), session.ID, playback.PlayTranscode, false); !errors.Is(err, playback.ErrHouseholdTranscodesExceeded) {
		t.Fatalf("switch error = %v, want the household pool refusal", err)
	}
	if got := refusals.all(); len(got) != 1 || got[0] != (refusal{7, "kid", playback.LimitTranscodes, playback.ScopeHousehold}) {
		t.Fatalf("refusals = %+v, want the household transcode refusal", got)
	}
}

// A session rebuilt after a restart is refused when it would go over the
// limit; Bloem's reconstruct reservation reports it.
func TestRebuildRefusalIsHeard(t *testing.T) {
	refusals := &refusalRecorder{}
	mgr := playback.NewSessionManager(0, 0)
	mgr.SetProfileLimitProvider(func(context.Context, int, string) (playback.SessionLimits, error) {
		return playback.SessionLimits{MaxTranscodes: 1}, nil
	})
	mgr.SetLimitRefusalObserver(refusals)
	if _, err := mgr.StartSession(7, "kid", 100, playback.PlayTranscode, false); err != nil {
		t.Fatal(err)
	}
	rebuilt := &playback.Session{ID: "rebuilt-1", UserID: 7, ProfileID: "kid", MediaFileID: 101, PlayMethod: playback.PlayTranscode}
	if _, err := mgr.RegisterReconstructedWithLimits(context.Background(), rebuilt); !errors.Is(err, playback.ErrTooManyTranscodes) {
		t.Fatalf("rebuild error = %v, want ErrTooManyTranscodes", err)
	}
	if got := refusals.all(); len(got) != 1 || got[0] != (refusal{7, "kid", playback.LimitTranscodes, playback.ScopePerson}) {
		t.Fatalf("refusals = %+v, want the rebuild's transcode refusal", got)
	}
}
