package notifications

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
	"github.com/Silo-Server/silo-server/internal/requests"
)

type recordingSink struct {
	mu      sync.Mutex
	notices []AdminPushNotice
	wakes   []string
}

func (s *recordingSink) Wake(_ context.Context, channel string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wakes = append(s.wakes, channel)
	return nil
}

func (s *recordingSink) Notify(_ context.Context, n AdminPushNotice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notices = append(s.notices, n)
	return nil
}

type recordingLifecycle struct{ submitted int }

func (r *recordingLifecycle) RequestSubmitted(context.Context, requests.Request) { r.submitted++ }
func (r *recordingLifecycle) RequestApproved(context.Context, requests.Request, requests.ApprovalOrigin) {
}
func (r *recordingLifecycle) RequestDeclined(context.Context, requests.Request) {}

func submitRequest(n *adminPushRequestNotifier, req requests.Request) {
	var wg sync.WaitGroup
	wg.Add(1)
	n.done = wg.Done
	n.RequestSubmitted(context.Background(), req)
	wg.Wait()
}

func TestAdminPushRequestDecoratorPushesOnlyWhatWaitsForAnAdmin(t *testing.T) {
	sink := &recordingSink{}
	inner := &recordingLifecycle{}
	n := &adminPushRequestNotifier{
		LifecycleNotifier: inner, push: sink,
		waiting: func(_ context.Context, requester int) int {
			if requester != 7 {
				t.Errorf("waiting asked about requester %d, want 7", requester)
			}
			return 4
		},
	}

	submitRequest(n, requests.Request{ID: "r1", Title: "Dune", Status: requests.StatusPending, RequestedByUserID: 7, RequesterUsername: "sam"})
	if inner.submitted != 1 {
		t.Fatal("the wrapped notifier (Discord and webhooks) must still run")
	}
	if len(sink.notices) != 1 {
		t.Fatalf("pending request pushed %d notices, want 1", len(sink.notices))
	}
	got := sink.notices[0]
	if got.Kind != AdminKindRequestWaiting || got.SubjectID != "r1" || got.Title != "sam requested Dune" || got.Body != "4 waiting" ||
		got.Badge == nil || *got.Badge != 4 || got.Audience != AudienceRequestAdmins || got.RequesterUserID != 7 {
		t.Fatalf("notice = %+v", got)
	}

	// Auto-approved: Silo still reports the submission, but nobody has to act, so nothing is notified.
	// The requests widget still changed, so it is woken, as it was for the pending one.
	submitRequest(n, requests.Request{ID: "r2", Title: "Dune", Status: requests.StatusApproved, RequestedByUserID: 7})
	if inner.submitted != 2 || len(sink.notices) != 1 {
		t.Fatalf("auto-approved request: wrapped=%d pushed=%d, want 2 and 1", inner.submitted, len(sink.notices))
	}
	if len(sink.wakes) != 2 || sink.wakes[0] != AdminWidgetRequests || sink.wakes[1] != AdminWidgetRequests {
		t.Fatalf("wakes for two arriving requests = %v, want requests twice", sink.wakes)
	}

	// Without a service the router keeps the notifier it had.
	if got := NewAdminPushRequestNotifier(inner, nil, nil); got != requests.LifecycleNotifier(inner) {
		t.Fatal("no push service should leave the lifecycle notifier untouched")
	}
}

func TestAdminReasonKeepsPathsOffTheLockScreen(t *testing.T) {
	for in, want := range map[string]string{
		"open /mnt/media/movies: no such file or directory": "open no such file or directory",
		`stat "/srv/library": permission denied`:            "stat permission denied",
		`C:\media\tv unreachable`:                           "unreachable",
		"~/library gone":                                    "gone",
		"   ":                                               "fallback",
		"/only/a/path":                                      "fallback",
	} {
		if got := adminReason(in, "fallback"); got != want {
			t.Errorf("adminReason(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("word ", 80)
	if got := adminText(long); len([]rune(got)) > adminTextMax {
		t.Fatalf("adminText kept %d characters", len([]rune(got)))
	}
	if got := adminText("a\nb\x00c\td"); got != "a bc d" {
		t.Fatalf("adminText control characters = %q", got)
	}
}

func TestAdminOutageTrackerFiresOncePerOutageAfterGrace(t *testing.T) {
	tr := newOutageTracker()
	t0 := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	if _, fire := tr.observe("n", true, t0, 2*time.Minute); fire {
		t.Fatal("fired before the grace period")
	}
	if _, fire := tr.observe("n", true, t0.Add(time.Minute), 2*time.Minute); fire {
		t.Fatal("fired inside the grace period")
	}
	start, fire := tr.observe("n", true, t0.Add(3*time.Minute), 2*time.Minute)
	if !fire || !start.Equal(t0) {
		t.Fatalf("fire=%v start=%v, want a fire for the outage that began at %v", fire, start, t0)
	}
	if _, fire := tr.observe("n", true, t0.Add(30*time.Minute), 2*time.Minute); fire {
		t.Fatal("a continuing outage fired twice")
	}
	tr.observe("n", false, t0.Add(31*time.Minute), 2*time.Minute)
	tr.observe("n", true, t0.Add(40*time.Minute), 2*time.Minute)
	if _, fire := tr.observe("n", true, t0.Add(43*time.Minute), 2*time.Minute); !fire {
		t.Fatal("a new outage after recovery did not fire")
	}
	// Replicas that noticed the same outage seconds apart derive one bucket.
	a := tr.bucket(t0.Add(10 * time.Second))
	b := tr.bucket(t0.Add(50 * time.Second))
	if !a.Equal(b) {
		t.Fatalf("buckets differ for one outage: %v vs %v", a, b)
	}
}

func TestAdminPushCollapseAndDeliveryIDs(t *testing.T) {
	down := AdminPushNotice{Kind: AdminKindServerDown, SubjectType: "server", SubjectID: "s1"}
	up := AdminPushNotice{Kind: AdminKindServerUp, SubjectType: "server", SubjectID: "s1"}
	if adminCollapseKey(down) != adminCollapseKey(up) {
		t.Fatal("down and up for one server must replace each other")
	}
	if adminCollapseKey(down) == adminCollapseKey(AdminPushNotice{Kind: AdminKindServerDown, SubjectType: "server", SubjectID: "s2"}) {
		t.Fatal("different servers share a collapse id")
	}
	a := NodeOfflineNotice("4", "Encoder", time.Unix(1000, 0))
	b := NodeOfflineNotice("4", "Encoder", time.Unix(5000, 0))
	if adminCollapseKey(a) != adminCollapseKey(b) {
		t.Fatal("a flapping node must reuse one collapse id")
	}
	if adminDeliveryID(a, 1) == adminDeliveryID(b, 1) || adminDeliveryID(a, 1) == adminDeliveryID(a, 2) {
		t.Fatal("delivery ids must differ per occurrence and per recipient")
	}
	if adminDeliveryID(a, 1) != adminDeliveryID(NodeOfflineNotice("4", "Encoder", time.Unix(1000, 0)), 1) {
		t.Fatal("delivery ids must be stable for the same event")
	}
	if got := len(adminCollapseKey(a)); got > 64 {
		t.Fatalf("collapse id is %d bytes, the relay allows 64", got)
	}
}

func TestAdminPushKindCatalogMatchesTheSpec(t *testing.T) {
	want := map[string]struct {
		channel, lock string
		actions       []string
		server        bool
	}{
		"admin.server_down":             {"servers", "A server can't be reached", nil, false},
		"admin.server_up":               {"servers", "A server is back", nil, false},
		"admin.request_waiting":         {"requests", "New request waiting", []string{"approve", "decline"}, true},
		"admin.scan_failed":             {"libraries", "A library scan failed", nil, true},
		"admin.backup_failed":           {"backups", "A backup failed", nil, true},
		"admin.node_offline":            {"nodes", "A node is offline", nil, true},
		"admin.plugin_failed":           {"plugins", "A plugin needs attention", nil, true},
		"admin.stream_limit_reached":    {"limits", "Someone was turned away at a stream limit", nil, true},
		"admin.transcode_limit_reached": {"limits", "Someone was turned away at a transcode limit", nil, true},
	}
	kinds := AdminPushKinds()
	if len(kinds) != len(want) {
		t.Fatalf("%d kinds, want %d", len(kinds), len(want))
	}
	for _, k := range kinds {
		w, ok := want[k.ID]
		if !ok || k.Channel != w.channel || k.LockText != w.lock || k.ServerEmitted != w.server || strings.Join(k.Actions, ",") != strings.Join(w.actions, ",") {
			t.Errorf("kind %s = %+v, want %+v", k.ID, k, w)
		}
		for _, action := range k.Actions {
			if action != AdminActionApprove && action != AdminActionDecline {
				t.Errorf("kind %s offers %q; nothing destructive may be offered", k.ID, action)
			}
		}
	}
	// A kind the apps produce locally cannot be emitted by the server.
	f := &AdminPushService{}
	if err := f.Notify(context.Background(), AdminPushNotice{Kind: AdminKindServerDown}); err != nil {
		t.Fatalf("an unavailable service must ignore events, got %v", err)
	}
}

func TestAdminPushWatchersEmitFromServerState(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.svc.SetServerName(func(context.Context) string { return "Home" })
	deliveries := func(kind string) int {
		return f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, kind)
	}

	// Backups: only a fresh failed run, once.
	run := AdminBackupRun{ID: "run-1", Status: "failed", Error: "write /backups/x: disk full", FinishedAt: f.now.Add(-time.Minute)}
	f.svc.watchBackup(ctx, func(context.Context) (AdminBackupRun, bool, error) { return run, true, nil }, f.now, nil)
	f.svc.watchBackup(ctx, func(context.Context) (AdminBackupRun, bool, error) { return run, true, nil }, f.now.Add(time.Minute), nil)
	if got := deliveries(AdminKindBackupFailed); got != 1 {
		t.Fatalf("a failed run produced %d deliveries over two polls, want 1", got)
	}
	var body string
	if err := f.pool.QueryRow(ctx, `SELECT title || ' / ' || body FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "Backup failed on Home / write disk full" {
		t.Fatalf("backup text %q, want the server name and the reason with its path removed", body)
	}
	f.svc.watchBackup(ctx, func(context.Context) (AdminBackupRun, bool, error) {
		return AdminBackupRun{ID: "old", Status: "failed", FinishedAt: f.now.Add(-48 * time.Hour)}, true, nil
	}, f.now, nil)
	f.svc.watchBackup(ctx, func(context.Context) (AdminBackupRun, bool, error) {
		return AdminBackupRun{ID: "ok", Status: "complete", FinishedAt: f.now}, true, nil
	}, f.now, nil)
	if got := deliveries(AdminKindBackupFailed); got != 1 {
		t.Fatalf("an old failure or a success produced a delivery (%d)", got)
	}

	// Nodes: unhealthy past the grace period, once; a disabled node is ignored.
	if _, err := f.pool.Exec(ctx, `INSERT INTO stream_nodes (id, name, type, url, enabled, healthy) VALUES
		(41, 'Encoder', 'transcode', 'https://node-a.example.test', true, false),
		(42, 'Spare', 'transcode', 'https://node-b.example.test', false, false),
		(43, 'Edge', 'proxy', 'https://node-c.example.test', true, true)`); err != nil {
		t.Fatal(err)
	}
	nodes := newOutageTracker()
	f.svc.watchNodes(ctx, nodes, nil, f.now)
	if got := deliveries(AdminKindNodeOffline); got != 0 {
		t.Fatalf("a node that just went down notified before the grace period (%d)", got)
	}
	f.svc.watchNodes(ctx, nodes, nil, f.now.Add(3*time.Minute))
	f.svc.watchNodes(ctx, nodes, nil, f.now.Add(4*time.Minute))
	if got := deliveries(AdminKindNodeOffline); got != 1 {
		t.Fatalf("node deliveries = %d, want 1 (one outage, one enabled unhealthy node)", got)
	}

	// Plugins: a failed one notifies at once, once per streak.
	plugins := newOutageTracker()
	list := func(failed bool) func(context.Context) ([]AdminPluginHealth, error) {
		return func(context.Context) ([]AdminPluginHealth, error) {
			return []AdminPluginHealth{
				{InstallationID: "9", Name: "Metadata", Failed: failed, Reason: "exit status 2 in /opt/plugins/meta"},
				{InstallationID: "10", Name: "Subtitles", Failed: false},
			}, nil
		}
	}
	f.svc.watchPlugins(ctx, list(true), plugins, f.now)
	f.svc.watchPlugins(ctx, list(true), plugins, f.now.Add(time.Minute))
	if got := deliveries(AdminKindPluginFailed); got != 1 {
		t.Fatalf("plugin deliveries = %d, want 1", got)
	}

	// Mounts: a dead_root warning after startup notifies; empty_root does not.
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_folders (id, type, name, enabled, scan_warning_code, scan_warning_message, scan_warning_at) VALUES
		(71, 'movies', 'Movies', true, 'dead_root', 'root /mnt/movies is not mounted', now()),
		(72, 'tv', 'Shows', true, 'empty_root', 'root looks empty', now())`); err != nil {
		t.Fatal(err)
	}
	since := f.now.Add(-time.Hour)
	f.svc.watchMounts(ctx, &since)
	f.svc.watchMounts(ctx, &since)
	if got := deliveries(AdminKindScanFailed); got != 1 {
		t.Fatalf("mount deliveries = %d, want 1 (dead_root only, once)", got)
	}
	var title, text string
	if err := f.pool.QueryRow(ctx, `SELECT title, body FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindScanFailed).Scan(&title, &text); err != nil {
		t.Fatal(err)
	}
	if title != "Scan failed: Movies" || strings.Contains(text, "/mnt") {
		t.Fatalf("mount notice = %q / %q", title, text)
	}
}

func TestAdminPushWatchScansTurnsAFailedScanEventIntoANotice(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := f.account("root", "admin", true)
	f.register(admin, fcmRegistration("install-root-droid", 'b'))
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_folders (id, type, name) VALUES (81, 'movies', 'Movies')`); err != nil {
		t.Fatal(err)
	}
	hub := evt.NewHub("test", nil)
	done := make(chan struct{})
	go func() { defer close(done); f.svc.WatchEvents(ctx, hub) }()

	publish := func(event string, run evt.ScanRun) {
		t.Helper()
		if err := hub.PublishJSON(ctx, evt.ChannelScans, event, run, evt.PublishOptions{AdminOnly: true}); err != nil {
			t.Fatal(err)
		}
	}
	// WatchScans subscribes asynchronously; keep publishing until it hears one.
	deadline := time.Now().Add(10 * time.Second)
	for f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindScanFailed) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no notice for a failed scan")
		}
		publish("scan.completed", evt.ScanRun{ID: "s0", LibraryID: 81, Status: "completed"})
		publish("scan.failed", evt.ScanRun{ID: "s1", LibraryID: 81, Status: "failed", ErrorMessage: "open /mnt/movies: input/output error"})
		<-time.After(50 * time.Millisecond)
	}
	cancel()
	<-done
	var title, body string
	if err := f.pool.QueryRow(context.Background(), `SELECT title, body FROM bloem_admin_push_deliveries WHERE kind = $1 LIMIT 1`, AdminKindScanFailed).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindScanFailed); got != 1 {
		t.Fatalf("repeated failures of one library in one window made %d deliveries, want 1", got)
	}
	if title != "Scan failed: Movies" || body != "open input/output error" {
		t.Fatalf("scan notice = %q / %q", title, body)
	}
}

// healthWoken reports whether the device has a health wake pending, then clears every pending wake.
func healthWoken(f *adminPushFixture, deviceID string) bool {
	f.t.Helper()
	got := strings.Join(f.pending(deviceID), ",")
	f.clearWakes()
	return got == AdminWidgetHealth
}

func TestAdminHealthWakeFollowsBackupFailuresOnce(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "health")
	states := newStateTracker()
	backup := func(states *stateTracker, id, status string, finished, at time.Time) {
		t.Helper()
		run := AdminBackupRun{ID: id, Status: status, FinishedAt: finished}
		f.svc.watchBackup(ctx, func(context.Context) (AdminBackupRun, bool, error) { return run, true, nil }, at, states)
	}
	start := f.now

	// A server that starts with a good backup has nothing new to show.
	backup(states, "run-0", "complete", start.Add(-time.Hour), start)
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("the first sight of a good backup woke the health widget")
	}
	backup(states, "run-1", "failed", start.Add(-time.Minute), start)
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a failed backup did not wake the health widget")
	}
	backup(states, "run-1", "failed", start.Add(-time.Minute), start.Add(time.Minute))
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("the same failed run seen on the next tick woke the widget again")
	}
	backup(states, "run-2", "complete", start.Add(time.Hour), start.Add(time.Hour))
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a backup that succeeded after a failure did not wake the health widget")
	}
	backup(states, "run-3", "complete_with_warnings", start.Add(2*time.Hour), start.Add(2*time.Hour))
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("a success after a success woke the widget")
	}
	backup(states, "run-4", "interrupted", start.Add(3*time.Hour), start.Add(3*time.Hour))
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("an interrupted backup did not wake the health widget")
	}
	backup(states, "run-5", "failed", start.Add(4*time.Hour), start.Add(4*time.Hour))
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a new failed run after an interrupted one did not wake the health widget")
	}

	// At start-up, a failure older than the freshness window is old news, as it is for the alert.
	restarted := newStateTracker()
	backup(restarted, "run-6", "failed", start.Add(-adminBackupFreshness-time.Minute), start)
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("an old failed run at start-up woke the health widget")
	}
	// The widget still shows that failure, so the success that replaces it is a change.
	backup(restarted, "run-7", "complete", start.Add(time.Hour), start.Add(time.Hour))
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a success after an old failure did not wake the health widget")
	}
}

func TestAdminHealthWakeFollowsPluginFailureAndRecovery(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "health")
	plugins := newOutageTracker()
	watch := func(failed bool) {
		t.Helper()
		f.svc.watchPlugins(ctx, func(context.Context) ([]AdminPluginHealth, error) {
			return []AdminPluginHealth{
				{InstallationID: "9", Name: "Metadata", Failed: failed, Reason: "exit status 2"},
				{InstallationID: "10", Name: "Subtitles"},
			}, nil
		}, plugins, f.now)
	}

	watch(false)
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("healthy plugins woke the health widget")
	}
	watch(true)
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a plugin turning failed did not wake the health widget")
	}
	watch(true)
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("a plugin staying failed woke the widget again")
	}
	watch(false)
	if !healthWoken(f, phone.DeviceID) {
		t.Fatal("a plugin recovering did not wake the health widget")
	}
	watch(false)
	if healthWoken(f, phone.DeviceID) {
		t.Fatal("a plugin staying healthy woke the widget")
	}
}

func TestAdminHealthWakeStillSendsTheAlert(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	alerted := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	quiet := f.register(admin, fcmRegistration("install-root-bbbb", 'b'))
	f.widgets(admin, alerted.DeviceID, "health")
	f.widgets(admin, quiet.DeviceID, "health")
	if _, err := f.svc.SetKinds(ctx, admin, quiet.DeviceID, map[string]bool{AdminKindBackupFailed: false}); err != nil {
		t.Fatal(err)
	}
	// The clock moves on every read, as a real one does, so the wake must be marked before the
	// alert is stored for the alert to cover it.
	f.svc.now = func() time.Time { f.now = f.now.Add(time.Millisecond); return f.now }
	states := newStateTracker()
	run := AdminBackupRun{ID: "run-1", Status: "failed", Error: "disk full", FinishedAt: f.now.Add(-time.Minute)}
	last := func(context.Context) (AdminBackupRun, bool, error) { return run, true, nil }
	f.svc.watchBackup(ctx, last, f.now, states)
	f.svc.watchBackup(ctx, last, f.now.Add(time.Minute), states)
	// FlushWakes reads the clock from several goroutines; hold it still from here on.
	f.svc.now = func() time.Time { return f.now }
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed); got != 1 {
		t.Fatalf("backup_failed deliveries = %d, want 1", got)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts a JOIN bloem_admin_push_devices d ON d.id = a.device_id
		WHERE d.server_device_id = $1`, alerted.DeviceID); got != 1 {
		t.Fatalf("the device with the alert on has %d attempts, want 1", got)
	}

	// The alert refreshes the widgets of the device it reaches; the device with the alert off gets the wake.
	f.now = f.now.Add(3 * time.Minute)
	f.flush()
	calls := f.relay.wakeCalls()
	if len(calls) != 1 {
		t.Fatalf("wake calls = %d, want 1 for the device without the alert", len(calls))
	}
	if id, _ := calls[0].Body["delivery_id"].(string); !strings.HasPrefix(id, quiet.DeviceID+".widgets-health-") {
		t.Fatalf("wake %q went to the wrong device or channel", id)
	}
}
