package livetv

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newRunningRecorder(t *testing.T) (*Service, *Recorder, *memoryStore, context.CancelFunc) {
	t.Helper()
	allowLoopbackMediaFetch(t)

	root := t.TempDir()
	store := newMemoryStore()
	store.channels["ch1"] = Channel{
		ID:        "ch1",
		TunerID:   "t1",
		Number:    "5.1",
		Name:      "Test",
		Enabled:   true,
		StreamURL: "http://127.0.0.1/auto/v5.1",
	}
	svc := NewServiceWithStore(store)
	now := time.Now().UTC().Truncate(time.Second)
	svc.now = func() time.Time { return now }
	recorder := NewRecorder(svc, filepath.Join(root, "dvr"), writeFakeFFmpeg(t, root, fakeFFmpegStayAlive))
	svc.SetRecorder(recorder)

	_, err := store.CreateRecording(context.Background(), &Recording{
		ID:        "rec-running",
		ChannelID: "ch1",
		Title:     "Lifecycle",
		Status:    "scheduled",
		Start:     now.Add(-time.Minute),
		Stop:      now.Add(2 * time.Minute),
		UserID:    1,
		ProfileID: "p",
	})
	if err != nil {
		t.Fatalf("CreateRecording: %v", err)
	}

	taskCtx, cancelTask := context.WithCancel(context.Background())
	started, _, _, err := svc.ProcessRecordings(taskCtx)
	if err != nil {
		cancelTask()
		t.Fatalf("ProcessRecordings: %v", err)
	}
	if started != 1 {
		cancelTask()
		t.Fatalf("started = %d, want 1", started)
	}
	return svc, recorder, store, cancelTask
}

func TestRecorderTaskCancellationDoesNotStopActiveRecording(t *testing.T) {
	_, recorder, _, cancelTask := newRunningRecorder(t)

	recorder.mu.Lock()
	session := recorder.active["rec-running"]
	recorder.mu.Unlock()
	if session == nil {
		t.Fatal("recording session was not registered")
	}

	cancelTask()
	select {
	case <-session.Done():
		t.Fatal("scheduler task cancellation stopped the active recording")
	case <-time.After(250 * time.Millisecond):
		// The process lifetime belongs to Recorder, not this scheduler tick.
	}

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClose()
	if err := recorder.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestRecorderCloseStopsProcessesWaitsAndRejectsNewWork(t *testing.T) {
	_, recorder, _, cancelTask := newRunningRecorder(t)
	defer cancelTask()

	recorder.mu.Lock()
	session := recorder.active["rec-running"]
	recorder.mu.Unlock()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelClose()
	if err := recorder.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("Close returned before the active process stopped")
	}

	if _, _, _, err := recorder.Process(context.Background()); !errors.Is(err, ErrRecorderClosed) {
		t.Fatalf("Process after Close error = %v, want ErrRecorderClosed", err)
	}
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRecorderCloseHonorsCallerDeadline(t *testing.T) {
	recorder := NewRecorder(nil, t.TempDir(), "")
	recorder.wg.Add(1)

	closeCtx, cancelClose := context.WithCancel(context.Background())
	cancelClose()
	if err := recorder.Close(closeCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v, want context.Canceled", err)
	}

	recorder.wg.Done()
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("Close after worker exit: %v", err)
	}
}

func TestServiceCloseWithoutRecorderIsSafe(t *testing.T) {
	if err := (&Service{}).Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
