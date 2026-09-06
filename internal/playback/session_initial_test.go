package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/userstore"
	"github.com/google/uuid"
)

func initialSessionBinding() InitialActivationBindingV3 {
	return InitialActivationBindingV3{Source: userstore.PlaybackSourceRef{Backend: "postgres", AccountID: 1, SourceID: uuid.NewString(), SelectionGeneration: 1}, Scope: userstore.PlaybackProgressScope{ProfileID: "profile", SessionID: uuid.NewString(), MediaItemID: "item"}, Fence: userstore.PlaybackProgressFence{AttemptID: uuid.NewString(), Incarnation: uuid.NewString(), OwnerID: uuid.NewString(), Epoch: 1}, IntentID: uuid.NewString(), AdmissionID: uuid.NewString()}
}

func TestInitialSessionInvisibleReservationAndPublication(t *testing.T) {
	m := NewSessionManager(1, 1)
	b := initialSessionBinding()
	stage, err := m.StageInitialSession(t.Context(), b, 2, 1, PlayTranscode, true)
	if err != nil {
		t.Fatal(err)
	}
	if stage.ID != b.Scope.SessionID || stage.UserID != b.Source.AccountID || stage.ProfileID != b.Scope.ProfileID {
		t.Fatal("captured identity changed")
	}
	if _, err := m.GetSession(stage.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("staged session visible")
	}
	if len(m.AllSessions()) != 0 || len(m.GetUserSessions(1)) != 0 || len(m.GetSessionsByMediaFileID(2)) != 0 {
		t.Fatal("staged session appeared in listing")
	}
	if m.ActiveCount(1) != 1 || m.TranscodeCount(1) != 1 {
		t.Fatal("staged admission not counted")
	}
	if _, err := m.StartSession(1, "other", 3, PlayDirect, false); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("legacy start bypassed reservation: %v", err)
	}
	if err := m.StopSession(stage.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("legacy stop reached stage")
	}
	if expired := m.CleanInactive(time.Nanosecond, time.Nanosecond); len(expired) != 0 {
		t.Fatal("legacy expiration removed stage")
	}
	stage.ClientName = "configured"
	stage.TranscodeTransportID = uuid.NewString()
	stage.Executor = &ExecutorNamespaceV3{Incarnation: b.Fence.Incarnation, Epoch: b.Fence.Epoch, ExecutorID: uuid.NewString()}
	published, err := m.PublishInitialSession(t.Context(), b, *stage)
	if err != nil {
		t.Fatal(err)
	}
	if published.ClientName != "configured" || published.ID != stage.ID || len(m.AllSessions()) != 1 {
		t.Fatal("configured snapshot not published")
	}
	stage.ClientName = "mutated"
	stage.Executor.Epoch++
	live, err := m.GetSession(published.ID)
	if err != nil || live.ClientName != "configured" || live.Executor.Epoch != b.Fence.Epoch {
		t.Fatalf("detached snapshot mutated live session: %+v %v", live, err)
	}
	if err := m.DiscardInitialSession(t.Context(), b); !errors.Is(err, ErrInitialActivationConflictV3) {
		t.Fatalf("discard visible: %v", err)
	}
	if _, err := m.GetSession(stage.ID); err != nil {
		t.Fatal("discard removed published session")
	}
}

func TestInitialSessionExactReplayAndConfigurationIdentity(t *testing.T) {
	m := NewSessionManager(1, 1)
	b := initialSessionBinding()
	stage, err := m.StageInitialSession(t.Context(), b, 2, 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	stage.ClientName = "caller-only"
	replay, err := m.StageInitialSession(t.Context(), b, 2, 1, PlayDirect, false)
	if err != nil || replay.ClientName == stage.ClientName || replay.ID != stage.ID {
		t.Fatalf("stage replay: %+v %v", replay, err)
	}
	changed := b
	changed.Fence.Epoch++
	if _, err := m.StageInitialSession(t.Context(), changed, 2, 1, PlayDirect, false); !errors.Is(err, ErrInitialActivationConflictV3) {
		t.Fatalf("changed fence admitted: %v", err)
	}
	if err := m.DiscardInitialSession(t.Context(), changed); !errors.Is(err, ErrInitialActivationConflictV3) {
		t.Fatalf("changed binding discarded: %v", err)
	}
	for name, mutate := range map[string]func(*Session){
		"account": func(s *Session) { s.UserID++ }, "profile": func(s *Session) { s.ProfileID = "other" }, "file": func(s *Session) { s.MediaFileID++ }, "requested": func(s *Session) { s.RequestedMediaFileID++ }, "method": func(s *Session) { s.PlayMethod = PlayTranscode }, "audio": func(s *Session) { s.TranscodeAudio = true }, "id": func(s *Session) { s.ID = uuid.NewString() },
	} {
		t.Run(name, func(t *testing.T) {
			cp := *replay
			mutate(&cp)
			if _, err := m.PublishInitialSession(t.Context(), b, cp); !errors.Is(err, ErrInitialActivationConflictV3) {
				t.Fatalf("changed identity accepted: %v", err)
			}
		})
	}
	if err := m.DiscardInitialSession(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := m.DiscardInitialSession(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if m.ActiveCount(1) != 0 {
		t.Fatal("discard retained reservation")
	}
}

func TestInitialSessionPolicyAndConcurrentDuplicateAdmission(t *testing.T) {
	m := NewSessionManager(1, 1)
	b := initialSessionBinding()
	m.SetAdmissionDecider(func(_ context.Context, r AdmissionRequest) (AdmissionDecision, error) {
		return AdmissionDecision{Allowed: r.CurrentActiveStreams == 0, ReasonCode: AdmissionReasonMaxStreamsExceeded}, nil
	})
	done := make(chan error, 8)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { <-gate; _, err := m.StageInitialSession(t.Context(), b, 1, 1, PlayDirect, false); done <- err })
	}
	close(gate)
	wg.Wait()
	close(done)
	for err := range done {
		if err != nil {
			t.Fatal(err)
		}
	}
	if m.ActiveCount(1) != 1 {
		t.Fatal("duplicate stage reserved multiple slots")
	}
	other := initialSessionBinding()
	if _, err := m.StageInitialSession(t.Context(), other, 1, 1, PlayDirect, false); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("policy did not see staged count: %v", err)
	}
}

func TestInitialSessionPublishDiscardRaceAndReconstruction(t *testing.T) {
	m := NewSessionManager(1, 1)
	b := initialSessionBinding()
	stage, err := m.StageInitialSession(t.Context(), b, 1, 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.RegisterReconstructed(&Session{ID: stage.ID, UserID: 1}); got != nil {
		t.Fatal("reconstruction exposed staged ID")
	}
	if _, err := m.RegisterReconstructedWithLimits(t.Context(), &Session{ID: stage.ID, UserID: 1}); !errors.Is(err, ErrInitialActivationConflictV3) {
		t.Fatalf("limited reconstruction bypass: %v", err)
	}
	gate := make(chan struct{})
	publishDone, discardDone := make(chan error, 1), make(chan error, 1)
	go func() { <-gate; _, err := m.PublishInitialSession(t.Context(), b, *stage); publishDone <- err }()
	go func() { <-gate; discardDone <- m.DiscardInitialSession(t.Context(), b) }()
	close(gate)
	publishErr, discardErr := <-publishDone, <-discardDone
	if (publishErr == nil) == (discardErr == nil) {
		t.Fatalf("race did not have one winner: %v %v", publishErr, discardErr)
	}
	_, liveErr := m.GetSession(stage.ID)
	if publishErr == nil && liveErr != nil {
		t.Fatal("winning publication discarded")
	}
	if discardErr == nil && !errors.Is(liveErr, ErrSessionNotFound) {
		t.Fatal("discarded reservation became visible")
	}
}

func TestInitialSessionReplayDoesNotReadmitAndCanceledStageReservesNothing(t *testing.T) {
	m := NewSessionManager(1, 1)
	b := initialSessionBinding()
	stage, err := m.StageInitialSession(t.Context(), b, 1, 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	m.SetLimitProvider(func(context.Context, int) (SessionLimits, error) {
		return SessionLimits{}, errors.New("unavailable policy")
	})
	if replay, err := m.StageInitialSession(t.Context(), b, 1, 1, PlayDirect, false); err != nil || replay.ID != stage.ID {
		t.Fatalf("exact reservation readmitted: %+v %v", replay, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.StageInitialSession(canceled, initialSessionBinding(), 1, 1, PlayDirect, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stage: %v", err)
	}
	if m.ActiveCount(1) != 1 {
		t.Fatal("canceled stage consumed capacity")
	}
}
