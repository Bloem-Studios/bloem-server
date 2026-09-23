package livetv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testDVRTuner() Tuner {
	return Tuner{ID: "t1", Type: TunerTypeHDHomeRun, DeviceID: "d1", TunerCount: 2, Status: "ready"}
}

func activeTunerSessions(store *memoryStore) []LiveSession {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out []LiveSession
	for _, session := range store.sessions {
		if session.Status == "active" {
			out = append(out, session)
		}
	}
	return out
}

func patchRecording(store *memoryStore, id string, fn func(*Recording)) {
	store.mu.Lock()
	defer store.mu.Unlock()
	rec := store.recordings[id]
	fn(&rec)
	store.recordings[id] = rec
}

// dvrFixture is one replica's Service+Recorder over a shared store.
func dvrFixture(t *testing.T, store *memoryStore, root string, now time.Time) (*Service, *Recorder) {
	t.Helper()
	svc := NewServiceWithStore(store)
	svc.now = func() time.Time { return now }
	recorder := NewRecorder(svc, filepath.Join(root, "dvr"), writeFakeFFmpeg(t, root, fakeFFmpegStayAlive))
	svc.SetRecorder(recorder)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = recorder.Close(ctx)
	})
	return svc, recorder
}

func dvrStore(tunerCount int) *memoryStore {
	store := newMemoryStore()
	tuner := testDVRTuner()
	tuner.TunerCount = tunerCount
	store.tuners["t1"] = tuner
	store.channels["ch1"] = Channel{ID: "ch1", TunerID: "t1", Enabled: true, StreamURL: "http://127.0.0.1/auto/v1"}
	return store
}

func scheduleDue(t *testing.T, store *memoryStore, id string, now time.Time) {
	t.Helper()
	if _, err := store.CreateRecording(context.Background(), &Recording{
		ID: id, ChannelID: "ch1", Title: id, Status: "scheduled", UserID: 1, ProfileID: "p",
		Start: now.Add(-time.Minute), Stop: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
}

// Two API replicas tick the same scheduled recording: exactly one starts FFmpeg.
func TestRecorderTwoReplicasStartRecordingOnce(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(2)
	scheduleDue(t, store, "rec-1", now)
	svcA, recA := dvrFixture(t, store, root, now)
	svcB, recB := dvrFixture(t, store, root, now)

	startedA, _, _, errA := svcA.ProcessRecordings(context.Background())
	startedB, _, _, errB := svcB.ProcessRecordings(context.Background())
	if errA != nil || errB != nil {
		t.Fatalf("errs %v %v", errA, errB)
	}
	if startedA+startedB != 1 || startedA != 1 {
		t.Fatalf("started A=%d B=%d, want exactly one", startedA, startedB)
	}
	if len(recB.active) != 0 || len(recA.active) != 1 {
		t.Fatalf("active A=%d B=%d", len(recA.active), len(recB.active))
	}
	if n := len(activeTunerSessions(store)); n != 1 {
		t.Fatalf("tuner sessions = %d, want 1", n)
	}
	got, _ := store.GetRecording(context.Background(), "rec-1")
	if got.Status != "recording" || got.ClaimToken == "" || got.TunerSessionID == "" || got.Segments != 1 || got.Interrupted {
		t.Fatalf("row = %+v", got)
	}
}

// cancelOnClaimStore cancels the row at a chosen point, modelling a user
// cancel racing the DVR tick.
type cancelOnClaimStore struct {
	*memoryStore
	beforeClaim bool
	beforeMark  bool
}

func (s *cancelOnClaimStore) ClaimRecording(ctx context.Context, id, status, token, nodeID string, lease time.Duration) (*Recording, error) {
	if s.beforeClaim {
		_, _ = s.CancelRecording(ctx, id)
	}
	return s.memoryStore.ClaimRecording(ctx, id, status, token, nodeID, lease)
}

func (s *cancelOnClaimStore) MarkRecordingStarted(ctx context.Context, id, token, path, tunerSessionID string, lease time.Duration) (bool, error) {
	if s.beforeMark {
		_, _ = s.CancelRecording(ctx, id)
	}
	return s.memoryStore.MarkRecordingStarted(ctx, id, token, path, tunerSessionID, lease)
}

func TestRecorderCancelRacingStartIsNeverOverwritten(t *testing.T) {
	allowLoopbackMediaFetch(t)
	for _, tc := range []struct {
		name  string
		store func(*memoryStore) *cancelOnClaimStore
	}{
		{"between load and claim", func(m *memoryStore) *cancelOnClaimStore {
			return &cancelOnClaimStore{memoryStore: m, beforeClaim: true}
		}},
		{"between claim and start", func(m *memoryStore) *cancelOnClaimStore { return &cancelOnClaimStore{memoryStore: m, beforeMark: true} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			now := time.Now().UTC().Truncate(time.Second)
			mem := dvrStore(2)
			scheduleDue(t, mem, "rec-c", now)
			store := tc.store(mem)
			svc := NewServiceWithStore(store)
			svc.now = func() time.Time { return now }
			recorder := NewRecorder(svc, filepath.Join(root, "dvr"), writeFakeFFmpeg(t, root, fakeFFmpegStayAlive))
			svc.SetRecorder(recorder)
			defer func() { _ = recorder.Close(context.Background()) }()

			started, _, _, err := svc.ProcessRecordings(context.Background())
			if err != nil || started != 0 {
				t.Fatalf("started=%d err=%v", started, err)
			}
			got, _ := mem.GetRecording(context.Background(), "rec-c")
			if got.Status != "cancelled" {
				t.Fatalf("cancel overwritten: %+v", got)
			}
			if len(recorder.active) != 0 || len(recorder.claims) != 0 {
				t.Fatalf("recorder kept a process: active=%d claims=%d", len(recorder.active), len(recorder.claims))
			}
			if n := len(activeTunerSessions(mem)); n != 0 {
				t.Fatalf("tuner sessions leaked: %d", n)
			}
		})
	}
}

// A cancel while recording stops the process and is not turned into completed.
func TestRecorderCancelDuringRecordingStaysCancelled(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(2)
	scheduleDue(t, store, "rec-x", now)
	svc, recorder := dvrFixture(t, store, root, now)
	recorder.leaseRenew = 10 * time.Millisecond
	if started, _, _, err := svc.ProcessRecordings(context.Background()); err != nil || started != 1 {
		t.Fatalf("started=%d err=%v", started, err)
	}
	if _, err := svc.CancelRecording(context.Background(), "rec-x", 1, "p", true); err != nil {
		t.Fatal(err)
	}
	// The lease renewal notices the cancel without waiting for a tick.
	waitFor(t, "cancelled recording stopped", func() bool {
		recorder.mu.Lock()
		idle := len(recorder.active) == 0
		recorder.mu.Unlock()
		return idle && len(activeTunerSessions(store)) == 0
	})
	svc.now = func() time.Time { return now.Add(10 * time.Minute) }
	if _, completed, _, err := svc.ProcessRecordings(context.Background()); err != nil || completed != 0 {
		t.Fatalf("completed=%d err=%v", completed, err)
	}
	if got, _ := store.GetRecording(context.Background(), "rec-x"); got.Status != "cancelled" {
		t.Fatalf("cancel resurrected: %+v", got)
	}
}

// Recordings and live tunes share one tuner ledger.
func TestRecordingTunerSharedWithLiveTunes(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(1)
	scheduleDue(t, store, "rec-t", now)
	svc, _ := dvrFixture(t, store, root, now)

	live, err := svc.StartChannelSession(context.Background(), "ch1", 2, "q", ClientCapabilities{})
	if err != nil {
		t.Fatalf("live tune: %v", err)
	}
	if started, _, failed, err := svc.ProcessRecordings(context.Background()); err != nil || started != 0 || failed != 0 {
		t.Fatalf("with tuner busy started=%d failed=%d err=%v", started, failed, err)
	}
	got, _ := store.GetRecording(context.Background(), "rec-t")
	if got.Status != "scheduled" || got.LastError != ErrNoTuner.Error() {
		t.Fatalf("busy tuner row = %+v", got)
	}
	if _, err := svc.ReleaseSession(context.Background(), live.ID, 2, "q", true); err != nil {
		t.Fatal(err)
	}
	if started, _, _, err := svc.ProcessRecordings(context.Background()); err != nil || started != 1 {
		t.Fatalf("retry started=%d err=%v", started, err)
	}
	if _, err := svc.StartChannelSession(context.Background(), "ch1", 2, "q", ClientCapabilities{}); !errors.Is(err, ErrNoTuner) {
		t.Fatalf("live tune while recording err=%v, want ErrNoTuner", err)
	}
}

// The owner process died mid-recording: once its lease lapses another replica
// resumes into a new segment and the result is flagged partial.
func TestRecorderResumesOrphanedRecordingAsInterrupted(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(1)
	svc, recorder := dvrFixture(t, store, root, now)

	dead, err := store.CreateSession(context.Background(), SessionCreate{ChannelID: "ch1", TunerID: "t1", TunerIndex: 0, PlaybackSessionID: recordingPlaybackID("rec-o")})
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(root, "dvr", "orphan.ts")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("first-"), 0o644); err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-time.Second)
	if _, err := store.CreateRecording(context.Background(), &Recording{
		ID: "rec-o", ChannelID: "ch1", Title: "Orphan", Status: "recording", Path: base,
		Start: now.Add(-time.Minute), Stop: now.Add(3 * time.Minute),
		ClaimToken: "dead-process", LeaseUntil: &expired, TunerSessionID: dead.ID, Segments: 1,
	}); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }

	started, _, _, err := svc.ProcessRecordings(context.Background())
	if err != nil || started != 1 {
		t.Fatalf("resume started=%d err=%v", started, err)
	}
	got, _ := store.GetRecording(context.Background(), "rec-o")
	if got.Status != "recording" || !got.Interrupted || got.Segments != 2 || got.ClaimToken == "dead-process" {
		t.Fatalf("resumed row = %+v", got)
	}
	if old, _ := store.GetSession(context.Background(), dead.ID); old.Status != "released" {
		t.Fatalf("dead owner's tuner still held: %+v", old)
	}
	part := segmentPath(base, 1)
	waitFor(t, "resumed segment written", func() bool {
		st, err := os.Stat(part)
		return err == nil && st.Size() > 0
	})

	svc.now = func() time.Time { return now.Add(5 * time.Minute) }
	_, completed, _, err := svc.ProcessRecordings(context.Background())
	if err != nil || completed != 1 {
		t.Fatalf("finish completed=%d err=%v", completed, err)
	}
	got, _ = store.GetRecording(context.Background(), "rec-o")
	if got.Status != "completed" || got.LastError != interruptedRecordingNote {
		t.Fatalf("finished = %+v", got)
	}
	data, _ := os.ReadFile(base)
	if len(data) <= len("first-") || string(data[:len("first-")]) != "first-" {
		t.Fatalf("segments not merged: %q", data)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("segment file left behind: %v", err)
	}
	if len(recorder.active) != 0 || len(activeTunerSessions(store)) != 0 {
		t.Fatal("finished recording still holds a process or tuner")
	}
}

// A recording whose owner still holds a live lease belongs to that replica.
func TestRecorderLeavesLiveOwnerAlone(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(2)
	svc, recorder := dvrFixture(t, store, root, now)
	lease := now.Add(time.Hour)
	if _, err := store.CreateRecording(context.Background(), &Recording{
		ID: "rec-l", ChannelID: "ch1", Title: "Live", Status: "recording", Path: filepath.Join(root, "x.ts"),
		Start: now.Add(-time.Minute), Stop: now.Add(-time.Second),
		ClaimToken: "other-replica", LeaseUntil: &lease, Segments: 1,
	}); err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	started, completed, failed, err := svc.ProcessRecordings(context.Background())
	if err != nil || started+completed+failed != 0 || len(recorder.active) != 0 {
		t.Fatalf("touched a live owner's row: started=%d completed=%d failed=%d err=%v", started, completed, failed, err)
	}
	if got, _ := store.GetRecording(context.Background(), "rec-l"); got.Status != "recording" || got.ClaimToken != "other-replica" {
		t.Fatalf("row = %+v", got)
	}
}

// If another replica took the recording over (our lease lapsed), the local
// FFmpeg is stopped at the next renewal instead of double-recording.
func TestRecorderStopsWhenClaimTakenOver(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(2)
	scheduleDue(t, store, "rec-s", now)
	svc, recorder := dvrFixture(t, store, root, now)
	recorder.leaseRenew = 10 * time.Millisecond
	if started, _, _, err := svc.ProcessRecordings(context.Background()); err != nil || started != 1 {
		t.Fatalf("started=%d err=%v", started, err)
	}
	patchRecording(store, "rec-s", func(r *Recording) { r.ClaimToken = "usurper" })
	waitFor(t, "local process stopped", func() bool {
		recorder.mu.Lock()
		defer recorder.mu.Unlock()
		return len(recorder.active) == 0 && len(recorder.claims) == 0
	})
	if got, _ := store.GetRecording(context.Background(), "rec-s"); got.Status != "recording" || got.ClaimToken != "usurper" {
		t.Fatalf("row rewritten by fenced owner: %+v", got)
	}
}

// Graceful shutdown hands the recording back so another replica resumes it.
func TestRecorderCloseReleasesClaimForResume(t *testing.T) {
	allowLoopbackMediaFetch(t)
	root := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	store := dvrStore(2)
	scheduleDue(t, store, "rec-g", now)
	svc, recorder := dvrFixture(t, store, root, now)
	if started, _, _, err := svc.ProcessRecordings(context.Background()); err != nil || started != 1 {
		t.Fatalf("started=%d err=%v", started, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRecording(context.Background(), "rec-g")
	if got.Status != "recording" || got.ClaimToken != "" || got.LeaseUntil != nil {
		t.Fatalf("claim not released on shutdown: %+v", got)
	}
	if n := len(activeTunerSessions(store)); n != 0 {
		t.Fatalf("tuner sessions after shutdown = %d", n)
	}
}

func TestMergeSegments(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "show.ts")
	for n, body := range map[int]string{2: "c", 10: "d", 1: "b"} {
		if err := os.WriteFile(segmentPath(base, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Base missing (recorded on another node): parts still merge in order.
	size, err := mergeSegments(base)
	if err != nil || size != 3 {
		t.Fatalf("size=%d err=%v", size, err)
	}
	if data, _ := os.ReadFile(base); string(data) != "bcd" {
		t.Fatalf("merged = %q", data)
	}
	if _, err := mergeSegments(""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := mergeSegments(filepath.Join(dir, "none.ts")); err == nil {
		t.Fatal("missing file accepted")
	}
	if segmentPath(base, 0) != base || segmentPath(base, 3) != filepath.Join(dir, "show.part3.ts") {
		t.Fatalf("segmentPath = %s", segmentPath(base, 3))
	}
}

func TestPgRecordingClaimsAreCompareAndSwap(t *testing.T) {
	dsn := os.Getenv("SILO_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("SILO_REQUIRE_TEST_DATABASE") == "1" {
			t.Fatal("SILO_TEST_DATABASE_URL is required")
		}
		t.Skip("SILO_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := NewPgStore(pool)
	tuner, err := store.CreateTuner(ctx, &Tuner{Type: TunerTypeHDHomeRun, DeviceID: uuid.NewString(), TunerCount: 1, Status: "ready", BaseURL: "http://192.168.1.2"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteTuner(ctx, tuner.ID) }()
	if err := store.ReplaceChannelsForTuner(ctx, tuner.ID, []Channel{{ID: uuid.NewString(), TunerID: tuner.ID, Number: "1", Name: "c", Enabled: true, StreamURL: "http://192.168.1.2/auto/v1"}}); err != nil {
		t.Fatal(err)
	}
	channels, err := store.ListChannels(ctx, tuner.ID)
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels %v %v", channels, err)
	}
	now := time.Now().UTC()
	rec, err := store.CreateRecording(ctx, &Recording{ChannelID: channels[0].ID, Title: "cas", Start: now.Add(-time.Minute), Stop: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	a, err := store.ClaimRecording(ctx, rec.ID, "scheduled", "tok-a", "node-a", time.Minute)
	if err != nil || a == nil || a.ClaimToken != "tok-a" || a.LeaseUntil == nil {
		t.Fatalf("claim a = %+v err=%v", a, err)
	}
	if b, err := store.ClaimRecording(ctx, rec.ID, "scheduled", "tok-b", "node-b", time.Minute); err != nil || b != nil {
		t.Fatalf("second claim while leased = %+v err=%v", b, err)
	}
	if ok, err := store.MarkRecordingStarted(ctx, rec.ID, "tok-b", "/x.ts", "s", time.Minute); err != nil || ok {
		t.Fatalf("non-owner mark ok=%v err=%v", ok, err)
	}
	if ok, err := store.ReleaseRecordingClaim(ctx, rec.ID, "tok-a", "boom"); err != nil || !ok {
		t.Fatalf("release ok=%v err=%v", ok, err)
	}
	got, _ := store.GetRecording(ctx, rec.ID)
	if got.Status != "scheduled" || got.StartAttempts != 1 || got.LastError != "boom" || got.LeaseUntil != nil {
		t.Fatalf("after release = %+v", got)
	}
	if b, err := store.ClaimRecording(ctx, rec.ID, "scheduled", "tok-b", "node-b", time.Minute); err != nil || b == nil {
		t.Fatalf("claim after release = %+v err=%v", b, err)
	}
	if ok, err := store.MarkRecordingStarted(ctx, rec.ID, "tok-b", "/x.ts", "sess", time.Minute); err != nil || !ok {
		t.Fatalf("mark ok=%v err=%v", ok, err)
	}
	got, _ = store.GetRecording(ctx, rec.ID)
	if got.Status != "recording" || got.Segments != 1 || got.Interrupted || got.Path != "/x.ts" || got.TunerSessionID != "sess" {
		t.Fatalf("after mark = %+v", got)
	}
	if ok, err := store.RenewRecordingLease(ctx, rec.ID, "tok-b", time.Minute); err != nil || !ok {
		t.Fatalf("renew ok=%v err=%v", ok, err)
	}

	// Expired lease: a second replica takes over and the resumed segment is
	// flagged interrupted; the fenced owner can no longer write.
	if _, err := pool.Exec(ctx, `UPDATE livetv_recordings SET lease_until = now() - interval '1 second' WHERE id = $1`, rec.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := store.ClaimRecording(ctx, rec.ID, "recording", "tok-c", "node-c", time.Minute); err != nil || c == nil || c.Segments != 1 {
		t.Fatalf("takeover = %+v err=%v", c, err)
	}
	if ok, _ := store.RenewRecordingLease(ctx, rec.ID, "tok-b", time.Minute); ok {
		t.Fatal("fenced owner renewed")
	}
	if ok, err := store.MarkRecordingStarted(ctx, rec.ID, "tok-c", "/x.ts", "sess2", time.Minute); err != nil || !ok {
		t.Fatalf("resume mark ok=%v err=%v", ok, err)
	}
	got, _ = store.GetRecording(ctx, rec.ID)
	if got.Segments != 2 || !got.Interrupted {
		t.Fatalf("after resume = %+v", got)
	}

	// A user cancel wins over every later owner write.
	if _, err := store.CancelRecording(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := store.RenewRecordingLease(ctx, rec.ID, "tok-c", time.Minute); ok {
		t.Fatal("renewed a cancelled recording")
	}
	if ok, _ := store.FinishRecordingClaim(ctx, rec.ID, "tok-c", "completed", "", ""); ok {
		t.Fatal("completed a cancelled recording")
	}
	if ok, _ := store.ReleaseRecordingClaim(ctx, rec.ID, "tok-c", "x"); ok {
		t.Fatal("released a cancelled recording")
	}
	if got, _ = store.GetRecording(ctx, rec.ID); got.Status != "cancelled" {
		t.Fatalf("cancel overwritten: %+v", got)
	}
}
