package livetv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLiveProcess struct {
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	closes    int
}

func newFakeLiveProcess() *fakeLiveProcess {
	return &fakeLiveProcess{done: make(chan struct{})}
}

func (p *fakeLiveProcess) Done() <-chan struct{} {
	return p.done
}

func (p *fakeLiveProcess) Err() error {
	return errors.New("ffmpeg exited")
}

func (p *fakeLiveProcess) exit() {
	p.closeOnce.Do(func() { close(p.done) })
}

func (p *fakeLiveProcess) Close() error {
	p.mu.Lock()
	p.closes++
	p.mu.Unlock()
	p.exit()
	return nil
}

func (p *fakeLiveProcess) closeCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closes
}

// testBridge returns a bridge that cleans up synchronously and does not check
// session rows unless a test asks it to.
func testBridge(t *testing.T) *HLSBridge {
	t.Helper()
	b := NewHLSBridge(HLSBridgeOptions{Root: t.TempDir()})
	b.cleanupDelay = 0
	b.leaseCheckInterval = 0
	return b
}

func trackedBridgeSession(t *testing.T, b *HLSBridge, id string) (*fakeLiveProcess, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !b.acquireTranscodeSlot(1) {
		t.Fatal("could not take the transcode slot")
	}
	proc := newFakeLiveProcess()
	b.trackSession(id, &bridgeSession{live: proc, dir: dir, userID: 7, profileID: "p1", holdsTranscodeSlot: true})
	return proc, dir
}

func bridgeHasSession(b *HLSBridge, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.sessions[id]
	return ok
}

// A remux whose ffmpeg died used to stay registered: it kept its encode slot and
// directory, and a client polling the frozen playlist kept renewing the tuner
// lease, so the tuner was never reclaimed.
func TestHLSBridgeReapsExitedRemux(t *testing.T) {
	b := testBridge(t)
	proc, dir := trackedBridgeSession(t, b, "dead")

	proc.exit()

	// Requests stop authorizing at once, so they cannot renew the lease.
	if err := b.Authorize("dead", 7, "p1", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Authorize on exited remux = %v, want ErrNotFound", err)
	}
	if _, err := b.ResolvePlaylistFile("dead", "index.m3u8"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolvePlaylistFile on exited remux = %v, want ErrNotFound", err)
	}
	waitFor(t, "slot release", func() bool { return b.acquireTranscodeSlot(1) })
	waitFor(t, "session removal", func() bool { return !bridgeHasSession(b, "dead") })
	waitFor(t, "directory cleanup", func() bool {
		_, err := os.Stat(dir)
		return os.IsNotExist(err)
	})
}

// StopLiveStream and the supervisor both see the process exit; only one may
// return the slot, or the encode cap drifts below its configured value.
func TestHLSBridgeStopReleasesSlotOnce(t *testing.T) {
	b := testBridge(t)
	proc, _ := trackedBridgeSession(t, b, "live")
	if err := b.Authorize("live", 7, "p1", true); err != nil {
		t.Fatalf("Authorize on running remux: %v", err)
	}
	// Hold a second slot so a double release would be observable.
	if !b.acquireTranscodeSlot(2) {
		t.Fatal("could not take the second slot")
	}

	if err := b.StopLiveStream(context.Background(), "live"); err != nil {
		t.Fatalf("StopLiveStream: %v", err)
	}
	if got := proc.closeCount(); got != 1 {
		t.Fatalf("Close calls = %d, want 1", got)
	}
	b.mu.Lock()
	active := b.activeTranscodes
	b.mu.Unlock()
	if active != 1 {
		t.Fatalf("activeTranscodes = %d, want 1 (the second slot)", active)
	}
}

// ledgerStore adds the playback-session capability PgStore has to the
// in-memory store.
type ledgerStore struct {
	*memoryStore
	checkErr atomic.Pointer[error]
}

func (s *ledgerStore) PlaybackSessionActive(_ context.Context, playbackSessionID string) (bool, error) {
	if err := s.checkErr.Load(); err != nil {
		return false, *err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, session := range s.sessions {
		if session.PlaybackSessionID == playbackSessionID && session.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

func (s *ledgerStore) ReleaseSessionByPlaybackID(_ context.Context, playbackSessionID string) (*LiveSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if session.PlaybackSessionID == playbackSessionID && session.Status == "active" {
			now := time.Now().UTC()
			session.Status = "released"
			session.ReleasedAt = &now
			s.sessions[id] = session
			return &session, nil
		}
	}
	return nil, nil
}

func sessionStatus(store *memoryStore, id string) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sessions[id].Status
}

// ledgerFixture wires a bridge to a service whose store knows playback IDs and
// creates an active session row served by playbackID.
func ledgerFixture(t *testing.T, playbackID string) (*HLSBridge, *Service, *ledgerStore, *LiveSession) {
	t.Helper()
	_, mem := singleTunerService(t)
	store := &ledgerStore{memoryStore: mem}
	svc := NewServiceWithStore(store)
	b := testBridge(t)
	svc.SetPlaybackBridge(b)
	row, err := store.CreateSession(context.Background(), SessionCreate{
		ChannelID: "ch1", TunerID: "t1", TunerIndex: 0, UserID: 7, ProfileID: "p1", PlaybackSessionID: playbackID,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return b, svc, store, row
}

// A dead remux used to leave its session row active until StaleSessionTTL, so
// the tuner looked busy to every tune in the meantime.
func TestHLSBridgeExitReleasesSessionRowImmediately(t *testing.T) {
	b, _, store, row := ledgerFixture(t, "pb-exit")
	proc, _ := trackedBridgeSession(t, b, "pb-exit")

	proc.exit()

	waitFor(t, "session row release", func() bool { return sessionStatus(store.memoryStore, row.ID) == "released" })
	indices, err := store.ActiveSessionTunerIndices(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(indices) != 0 {
		t.Fatalf("tuner indices still claimed after the remux exited: %v", indices)
	}
}

// A release can land on a replica that does not run the remux. The replica that
// does must notice the row is gone and stop ffmpeg itself.
func TestHLSBridgeStopsRemuxWhenSessionReleasedElsewhere(t *testing.T) {
	b, _, store, row := ledgerFixture(t, "pb-remote")
	b.leaseCheckInterval = 5 * time.Millisecond
	proc, _ := trackedBridgeSession(t, b, "pb-remote")

	if _, err := store.ReleaseSession(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "remux stop", func() bool { return proc.closeCount() == 1 && !bridgeHasSession(b, "pb-remote") })
}

// A remux with no session row at all -- never recorded, or deleted -- is not
// kept alive on the assumption that one exists.
func TestHLSBridgeStopsRemuxWithoutSessionRow(t *testing.T) {
	b, _, _, _ := ledgerFixture(t, "pb-other")
	b.leaseCheckInterval = 5 * time.Millisecond
	proc, _ := trackedBridgeSession(t, b, "pb-orphan")

	waitFor(t, "orphan stop", func() bool { return proc.closeCount() == 1 && !bridgeHasSession(b, "pb-orphan") })
}

func TestHLSBridgeToleratesTransientSessionCheckErrors(t *testing.T) {
	b, _, store, _ := ledgerFixture(t, "pb-blip")
	storeErr := errors.New("database unavailable")
	store.checkErr.Store(&storeErr)
	b.leaseCheckInterval = 5 * time.Millisecond
	proc, _ := trackedBridgeSession(t, b, "pb-blip")

	time.Sleep(30 * time.Millisecond)
	if proc.closeCount() != 0 || !bridgeHasSession(b, "pb-blip") {
		t.Fatal("a transient store error stopped a running remux")
	}
	_ = b.StopLiveStream(context.Background(), "pb-blip")
}
