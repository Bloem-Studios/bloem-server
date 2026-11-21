package notifications

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

func scanRun(id string, library int, status string, startedAt time.Time, processed, total int) evt.ScanRun {
	started := startedAt
	return evt.ScanRun{ID: id, LibraryID: library, Status: status, StartedAt: &started,
		Result: &evt.ScanRunResult{FilesProcessed: processed, TotalFiles: total}}
}

func liveCalls(calls []relayCall) []relayCall {
	out := []relayCall{}
	for _, call := range calls {
		if call.Body["mode"] == relayModeLiveActivity {
			out = append(out, call)
		}
	}
	return out
}

func stateOf(t *testing.T, call relayCall) map[string]any {
	t.Helper()
	state, ok := call.Body["content_state"].(map[string]any)
	if !ok {
		t.Fatalf("no content_state in %v", call.Body)
	}
	return state
}

func TestScanProgressUpdatesAtMostEveryThirtySeconds(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	base := f.now
	for i := range 10 {
		f.now = base.Add(time.Duration(2*i) * time.Second)
		f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 10*(i+1), 100))
		f.svc.FlushLive(ctx)
	}
	calls := liveCalls(relay.received())
	if len(calls) != 1 {
		t.Fatalf("ten progress events in 20 s sent %d updates, want 1", len(calls))
	}
	if got := stateOf(t, calls[0]); got["phase"] != LivePhaseRunning || got["processed"] != float64(10) || got["total"] != float64(100) ||
		got["progress"] != 0.1 || got["libraryID"] != "7" {
		t.Fatalf("first update = %v", got)
	}
	f.now = base.Add(31 * time.Second)
	f.svc.FlushLive(ctx)
	calls = liveCalls(relay.received())
	if len(calls) != 2 {
		t.Fatalf("after 30 s the newest state did not go: %d updates", len(calls))
	}
	if got := stateOf(t, calls[1]); got["processed"] != float64(100) || calls[1].Body["priority"] != "low" {
		t.Fatalf("second update = %v (priority %v), want the newest state at low priority", got, calls[1].Body["priority"])
	}
}

func TestScanEndSendsTheFinalStateAndDismissal(t *testing.T) {
	for event, phase := range map[string]string{"scan.completed": LivePhaseFinished, "scan.failed": LivePhaseFailed, "scan.cancelled": LivePhaseFinished} {
		t.Run(event, func(t *testing.T) {
			f, relay := newLiveRelayFixture(t)
			ctx := context.Background()
			admin := f.account("root", "admin", true)
			device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
			f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
			base := f.now
			f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 10, 100))
			f.svc.FlushLive(ctx)
			// The end goes at once, inside the 30 s window.
			f.now = base.Add(5 * time.Second)
			status := strings.TrimPrefix(event, "scan.")
			f.svc.followScan(ctx, event, scanRun("run-1", 7, status, base, 100, 100))
			f.svc.FlushLive(ctx)
			calls := liveCalls(relay.received())
			if len(calls) != 2 {
				t.Fatalf("updates = %d, want the progress and the end", len(calls))
			}
			end := calls[1].Body
			if end["event"] != LiveEventEnd || end["priority"] != "high" || stateOf(t, calls[1])["phase"] != phase ||
				end["dismissal_date"] != float64(f.now.Add(15*time.Minute).Unix()) {
				t.Fatalf("end = %v", end)
			}
			// Nothing after the end.
			for i := 1; i <= 3; i++ {
				f.now = base.Add(time.Duration(i) * time.Minute)
				f.svc.followScan(ctx, "scan.progress", scanRun("run-2", 7, "running", f.now, i, 100))
				f.svc.followScan(ctx, "scan.failed", scanRun("run-2", 7, "failed", f.now, i, 100))
				f.svc.FlushLive(ctx)
			}
			if got := len(liveCalls(relay.received())); got != 2 {
				t.Fatalf("%d sends after the end", got-2)
			}
		})
	}
}

func TestTimestampsStrictlyIncrease(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	// Everything inside one second: a phase change and the end must still order.
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", f.now, 1, 10))
	f.svc.FlushLive(ctx)
	// A send that fails transiently goes again with a newer timestamp.
	relay.answerAll(503, `{"error":{"code":"upstream_unavailable","message":"try later"}}`, "")
	f.svc.followScan(ctx, "scan.failed", scanRun("run-1", 7, "failed", f.now, 5, 10))
	f.svc.FlushLive(ctx)
	relay.answerAll(200, relayAccepted, "")
	f.now = f.now.Add(31 * time.Second)
	f.svc.FlushLive(ctx)
	calls := liveCalls(relay.received())
	if len(calls) != 3 {
		t.Fatalf("sends = %d, want 3", len(calls))
	}
	last := 0.0
	for i, call := range calls {
		ts := call.Body["timestamp"].(float64)
		if ts <= last {
			t.Fatalf("send %d has timestamp %v after %v", i, ts, last)
		}
		last = ts
	}
	if calls[2].Body["event"] != LiveEventEnd {
		t.Fatalf("the retried send = %v, want the end", calls[2].Body["event"])
	}
}

// startOptIn registers an Apple device that allowed the server to start the kinds.
func (f *adminPushFixture) startOptIn(userID int, installation string, token byte, start string, kinds ...string) *AdminPushRegistered {
	f.t.Helper()
	device := f.register(userID, apnsRegistration(installation, token))
	if err := f.svc.SetLiveStart(context.Background(), userID, device.DeviceID, &start, kinds); err != nil {
		f.t.Fatalf("set start: %v", err)
	}
	return device
}

func startsTo(calls []relayCall) map[string]relayCall {
	out := map[string]relayCall{}
	for _, call := range liveCalls(calls) {
		if call.Body["event"] == LiveEventStart {
			out[call.Body["token"].(string)] = call
		}
	}
	return out
}

func TestPushStartAfterSixtySecondsOnlyForOptedInDevices(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	tokenFor := func(c byte) string { return strings.Repeat(string(c), 80) }
	optedIn := f.startOptIn(admin, "install-opted-in1", 'a', tokenFor('1'), LiveKindScan)
	f.startOptIn(admin, "install-backup-on1", 'b', tokenFor('2'), LiveKindBackup)
	alertOff := f.startOptIn(admin, "install-alert-off", 'c', tokenFor('3'), LiveKindScan)
	if _, err := f.svc.SetKinds(ctx, admin, alertOff.DeviceID, map[string]bool{AdminKindScanFailed: false}); err != nil {
		t.Fatal(err)
	}
	f.register(admin, apnsRegistration("install-no-start1", 'd'))
	showing := f.startOptIn(admin, "install-showing01", 'e', tokenFor('5'), LiveKindScan)
	f.registerLive(admin, showing.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	withdrawn := f.startOptIn(admin, "install-withdrawn", 'f', tokenFor('6'), LiveKindScan)
	if err := f.svc.SetLiveStart(ctx, admin, withdrawn.DeviceID, nil, nil); err != nil {
		t.Fatal(err)
	}

	base := f.now
	f.now = base.Add(30 * time.Second)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 1, 10))
	if got := startsTo(relay.received()); len(got) != 0 {
		t.Fatalf("a scan running 30 s was push-started on %d devices", len(got))
	}
	f.now = base.Add(61 * time.Second)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 2, 10))
	f.svc.waitLiveStarts()
	f.now = base.Add(90 * time.Second)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 3, 10))
	f.svc.waitLiveStarts()
	starts := startsTo(relay.received())
	if len(starts) != 1 {
		t.Fatalf("push-started on %d devices, want only the opted-in one: %v", len(starts), starts)
	}
	start, ok := starts[tokenFor('1')]
	if !ok {
		t.Fatal("the opted-in device got no start")
	}
	attributes := start.Body["attributes"].(map[string]any)
	if start.Body["attributes_type"] != "AdminJobAttributes" || attributes["kind"] != LiveKindScan ||
		attributes["pushServerID"] != optedIn.DeviceID || attributes["startedAt"] != float64(base.Unix()) {
		t.Fatalf("start = %v", start.Body)
	}
	if stateOf(t, start)["libraryID"] != "7" {
		t.Fatalf("start state = %v", stateOf(t, start))
	}
	if n := len(liveCalls(relay.received())) - countUpdates(relay.received()); n != 1 {
		t.Fatalf("%d starts went, want one per job and device", n)
	}
	// Another scan of the same library is another job: it may start again.
	f.now = base.Add(5 * time.Minute)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-2", 7, "running", base.Add(3*time.Minute), 3, 10))
	f.svc.waitLiveStarts()
	if n := len(liveCalls(relay.received())) - countUpdates(relay.received()); n != 2 {
		t.Fatalf("a second scan started %d times in all, want 2", n)
	}
}

func countUpdates(calls []relayCall) int {
	n := 0
	for _, call := range liveCalls(calls) {
		if call.Body["event"] != LiveEventStart {
			n++
		}
	}
	return n
}

func TestBackupFollowedAtOnce(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	optedIn := f.startOptIn(admin, "install-opted-in1", 'a', strings.Repeat("1", 80), LiveKindBackup)
	f.startOptIn(admin, "install-scan-only", 'b', strings.Repeat("2", 80), LiveKindScan)
	viewer := f.register(admin, apnsRegistration("install-viewer-01", 'c'))
	f.registerLive(admin, viewer.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenA))
	started := f.now.Add(-5 * time.Second)

	f.svc.followBackup(ctx, &AdminActiveBackup{ID: "bk-1", StartedAt: started, Progress: 40}, AdminBackupRun{}, false)
	f.svc.waitLiveStarts()
	f.svc.FlushLive(ctx)
	starts := startsTo(relay.received())
	if len(starts) != 1 {
		t.Fatalf("a backup was push-started on %d devices, want 1 at once", len(starts))
	}
	attributes := starts[strings.Repeat("1", 80)].Body["attributes"].(map[string]any)
	if attributes["kind"] != LiveKindBackup || attributes["pushServerID"] != optedIn.DeviceID || attributes["startedAt"] != float64(started.Unix()) {
		t.Fatalf("backup start attributes = %v", attributes)
	}
	var update *relayCall
	for _, call := range liveCalls(relay.received()) {
		if call.Body["token"] == liveTokenA {
			update = &call
		}
	}
	if update == nil || stateOf(t, *update)["progress"] != 0.4 || stateOf(t, *update)["phase"] != LivePhaseRunning {
		t.Fatalf("the registered backup activity was not updated: %v", update)
	}
	if _, ok := stateOf(t, *update)["libraryID"]; ok {
		t.Fatal("a backup state has no library")
	}

	// The backup ends: the latest run's outcome ends the activity.
	f.now = f.now.Add(2 * time.Minute)
	f.svc.followBackup(ctx, nil, AdminBackupRun{ID: "bk-1", Status: "failed", FinishedAt: f.now.Add(-10 * time.Second)}, true)
	f.svc.FlushLive(ctx)
	calls := liveCalls(relay.received())
	end := calls[len(calls)-1]
	if end.Body["token"] != liveTokenA || end.Body["event"] != LiveEventEnd || stateOf(t, end)["phase"] != LivePhaseFailed {
		t.Fatalf("backup end = %v", end.Body)
	}
}

func TestBackupEndDoesNotEndAnActivityStartedAfterTheRun(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-viewer-01", 'c'))
	// The app starts a backup and its activity; the watcher still sees the previous run.
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenA))
	f.svc.followBackup(ctx, nil, AdminBackupRun{ID: "old", Status: "complete", FinishedAt: f.now.Add(-3 * time.Hour)}, true)
	f.svc.FlushLive(ctx)
	if got := len(liveCalls(relay.received())); got != 0 {
		t.Fatalf("an old run ended a new activity (%d sends)", got)
	}
}

func TestNothingSentPastEightHours(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	base := f.now
	// A state queued just before the end of the lifetime is not sent after it.
	f.now = base.Add(adminLiveLifetime - time.Second)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 1, 10))
	f.now = base.Add(adminLiveLifetime + time.Second)
	f.svc.FlushLive(ctx)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 2, 10))
	f.svc.followScan(ctx, "scan.completed", scanRun("run-1", 7, "completed", base, 10, 10))
	f.svc.FlushLive(ctx)
	if got := len(liveCalls(relay.received())); got != 0 {
		t.Fatalf("%d sends to an activity past its 8 hours", got)
	}
}

func TestUnchangedStateIsNotSent(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	base := f.now
	for i := range 4 {
		f.now = base.Add(time.Duration(i) * 31 * time.Second)
		f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 5, 10))
		f.svc.FlushLive(ctx)
	}
	if got := len(liveCalls(relay.received())); got != 1 {
		t.Fatalf("the same state was sent %d times", got)
	}
	// A state that moves on and back before its send goes out sends nothing new.
	f.now = f.now.Add(5 * time.Second)
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 6, 10))
	f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 5, 10))
	f.now = f.now.Add(31 * time.Second)
	f.svc.FlushLive(ctx)
	if got := len(liveCalls(relay.received())); got != 1 {
		t.Fatalf("a state equal to the one shown was sent (%d sends)", got)
	}
}

func TestEveryReplicaSendsOnce(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	f.startOptIn(admin, "install-opted-in1", 'b', strings.Repeat("1", 80), LiveKindScan)
	replicas := []*AdminPushService{f.svc, f.newService(f.svc.sender, f.svc.settings, f.svc.cipher), f.newService(f.svc.sender, f.svc.settings, f.svc.cipher)}
	base := f.now
	f.now = base.Add(61 * time.Second)
	var wg sync.WaitGroup
	for _, replica := range replicas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Every replica hears the same event from the events bus.
			replica.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 3, 10))
			replica.waitLiveStarts()
			replica.FlushLive(ctx)
		}()
	}
	wg.Wait()
	calls := liveCalls(relay.received())
	if got := countUpdates(relay.received()); got != 1 {
		t.Fatalf("%d replicas sent the update, want 1", got)
	}
	if got := len(calls) - countUpdates(relay.received()); got != 1 {
		t.Fatalf("%d replicas push-started the job, want 1", got)
	}
}

func TestWatchEventsFollowsScans(t *testing.T) {
	f, _ := newLiveRelayFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	hub := evt.NewHub("test", nil)
	done := make(chan struct{})
	go func() { defer close(done); f.svc.WatchEvents(ctx, hub) }()
	deadline := time.Now().Add(10 * time.Second)
	for f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE pending_state IS NOT NULL`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("WatchEvents did not hand a scan's progress to the activity")
		}
		if err := hub.PublishJSON(ctx, evt.ChannelScans, "scan.progress", scanRun("run-1", 7, "running", f.now, 1, 10), evt.PublishOptions{AdminOnly: true}); err != nil {
			t.Fatal(err)
		}
		<-time.After(50 * time.Millisecond)
	}
	cancel()
	<-done
}

// ---- S1-4: cleanup ------------------------------------------------------------

func TestPrunerDeletesOldLiveActivities(t *testing.T) {
	f, _ := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	signedOut := f.startOptIn(admin, "install-signed-out", 'b', liveStart, LiveKindScan)
	base := f.now
	ids := map[string]string{
		"ended and sent":         "6f1c2b9e-1d4a-4c7e-9a2b-000000000201",
		"ended, end still owed":  "6f1c2b9e-1d4a-4c7e-9a2b-000000000202",
		"past lifetime + 4 h":    "6f1c2b9e-1d4a-4c7e-9a2b-000000000203",
		"past lifetime only":     "6f1c2b9e-1d4a-4c7e-9a2b-000000000204",
		"live":                   "6f1c2b9e-1d4a-4c7e-9a2b-000000000205",
		"device login has ended": "6f1c2b9e-1d4a-4c7e-9a2b-000000000206",
	}
	for name, id := range ids {
		on := device.DeviceID
		if name == "device login has ended" {
			on = signedOut.DeviceID
		}
		f.registerLive(admin, on, iosActivity(id, LiveKindBackup, "", liveTokenA))
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE bloem_admin_live_activities SET ended_at = $2 WHERE activity_id = $1`, ids["ended and sent"], base)
	exec(`UPDATE bloem_admin_live_activities SET ended_at = $2, pending_state = '{"phase":"finished","updatedAt":1}', pending_event = 'end'
		WHERE activity_id = $1`, ids["ended, end still owed"], base)
	exec(`UPDATE bloem_admin_live_activities SET created_at = $2, expires_at = $3 WHERE activity_id = $1`,
		ids["past lifetime + 4 h"], base.Add(-13*time.Hour), base.Add(-5*time.Hour))
	exec(`UPDATE bloem_admin_live_activities SET created_at = $2, expires_at = $3 WHERE activity_id = $1`,
		ids["past lifetime only"], base.Add(-9*time.Hour), base.Add(-time.Hour))
	// The login that registered this device has ended (signed out, or revoked).
	exec(`UPDATE bloem_admin_push_devices SET login_session_id = 'ended-login' WHERE server_device_id = $1`, signedOut.DeviceID)
	signedOutRow := f.deviceRowID(signedOut.DeviceID)
	exec(`INSERT INTO bloem_admin_live_starts (device_id, job_key, created_at) VALUES ($1, 'scan:old', $2), ($1, 'scan:new', $3)`,
		f.deviceRowID(device.DeviceID), base.Add(-13*time.Hour), base.Add(-time.Hour))

	f.svc.pruneLive(ctx)

	for name, id := range ids {
		kept := name == "ended, end still owed" || name == "past lifetime only" || name == "live"
		if got := f.liveCount(id) == 1; got != kept {
			t.Fatalf("%s: kept = %t, want %t", name, got, kept)
		}
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE id = $1 AND live_start_token_ciphertext IS NULL AND cardinality(live_start_kinds) = 0`, signedOutRow); got != 1 {
		t.Fatal("a device whose login ended must lose its push-to-start token")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_starts WHERE job_key = 'scan:old'`); got != 0 {
		t.Fatal("start claims past a job's lifetime must be deleted")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_starts WHERE job_key = 'scan:new'`); got != 1 {
		t.Fatal("a recent start claim was deleted")
	}
}

// The events hub drops events for a subscriber that falls behind, and the same loop
// sends scan failure alerts, so following a scan must never wait for the relay.
func TestFollowScanDoesNotWaitForTheRelay(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	f.startOptIn(admin, "install-opted-in1", 'a', strings.Repeat("1", 80), LiveKindScan)
	release := make(chan struct{})
	relay.mu.Lock()
	relay.answer = func(relayCall) (int, string, string) {
		<-release
		return 200, relayAccepted, ""
	}
	relay.mu.Unlock()
	base := f.now
	f.now = base.Add(2 * time.Minute)
	returned := make(chan struct{})
	go func() {
		f.svc.followScan(ctx, "scan.progress", scanRun("run-1", 7, "running", base, 1, 10))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("following a scan waited for the relay to answer a push start")
	}
	close(release)
	f.svc.waitLiveStarts()
	if got := len(startsTo(relay.received())); got != 1 {
		t.Fatalf("starts = %d, want the one that was waiting", got)
	}
}
