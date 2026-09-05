package transcodenode

import (
	"context"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/playback"
)

type executorRecordingTracker struct {
	recordingSessionTracker
	executorRemoved []playback.ExecutorNamespaceV3
}

func (tr *executorRecordingTracker) RemoveExecutor(_ context.Context, _ string, ns playback.ExecutorNamespaceV3) {
	tr.executorRemoved = append(tr.executorRemoved, ns)
}

func TestWorkerExecutorTrackingRetiresCapturedGeneration(t *testing.T) {
	s := newTestServer(t)
	tr := &executorRecordingTracker{}
	s.tracker = tr
	oldNS, nextNS := workerNamespace(), workerNamespace()
	old := workerBoundSession(t, s, oldNS)
	next := workerBoundSession(t, s, nextNS)
	s.sessions["worker-session"] = next
	s.lastAccess["worker-session"] = time.Now().Add(-time.Hour)
	s.reapSession("worker-session", old, time.Now())
	if len(tr.executorRemoved) != 0 {
		t.Fatal("stale reap removed successor tracking")
	}
	// A delayed cleanup already holding the retired object must use its identity,
	// even though the map now contains the successor.
	s.removeTrackedExecutor(t.Context(), "worker-session", old.ExecutorNamespace())
	if len(tr.executorRemoved) != 1 || tr.executorRemoved[0] != *oldNS || len(tr.removed) != 0 {
		t.Fatal("cleanup used successor or legacy tracker key")
	}
	s.activeJobs.Store(1)
	s.reapSession("worker-session", next, time.Now())
	if len(tr.executorRemoved) != 2 || tr.executorRemoved[1] != *nextNS || len(tr.removed) != 0 {
		t.Fatal("reap did not remove its own exact generation")
	}
}
