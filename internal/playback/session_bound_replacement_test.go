package playback

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestBoundSessionReplacementOnlyProjectsExactLivePredecessor(t *testing.T) {
	m := NewSessionManager(0, 0)
	binding := initialSessionBinding()
	stage, err := m.StageInitialSession(t.Context(), binding, 1, 1, PlayDirect, false)
	if err != nil {
		t.Fatal(err)
	}
	stage.Executor = &ExecutorNamespaceV3{Incarnation: binding.Fence.Incarnation, Epoch: binding.Fence.Epoch, ExecutorID: uuid.NewString()}
	stage.TranscodeTransportID = uuid.NewString()
	current, err := m.PublishInitialSession(t.Context(), binding, *stage)
	if err != nil {
		t.Fatal(err)
	}
	next := *current
	next.Executor = &ExecutorNamespaceV3{Incarnation: binding.Fence.Incarnation, Epoch: binding.Fence.Epoch, ExecutorID: uuid.NewString()}
	next.TranscodeTransportID = uuid.NewString()
	next.Position = 30
	stale := *current.Executor
	stale.ExecutorID = uuid.NewString()
	if _, err := m.PublishBoundSessionReplacement(t.Context(), binding, stale, current.TranscodeTransportID, next); !errors.Is(err, ErrInitialActivationConflictV3) {
		t.Fatal("stale predecessor accepted", err)
	}
	published, err := m.PublishBoundSessionReplacement(t.Context(), binding, *current.Executor, current.TranscodeTransportID, next)
	if err != nil || *published.Executor != *next.Executor || published.TranscodeTransportID != next.TranscodeTransportID || published.Position != 30 {
		t.Fatalf("projection: %+v %v", published, err)
	}
	if err := m.UpdateProgress(current.ID, 35, true); err != nil {
		t.Fatal(err)
	}
	replay, err := m.PublishBoundSessionReplacement(t.Context(), binding, *current.Executor, current.TranscodeTransportID, next)
	if err != nil || replay.Position != 35 || !replay.IsPaused {
		t.Fatal("projection replay reset progress", err)
	}
	if err := m.StopSession(current.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PublishBoundSessionReplacement(t.Context(), binding, *current.Executor, current.TranscodeTransportID, next); !errors.Is(err, ErrSessionNotFound) {
		t.Fatal("replacement recreated stopped session", err)
	}
}
