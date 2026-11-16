package notifications

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	evt "github.com/Silo-Server/silo-server/internal/events"
)

// wakeCalls are the relay calls that were silent wakes.
func (r *fakeAdminRelay) wakeCalls() []relayCall {
	var out []relayCall
	for _, c := range r.received() {
		if c.Body["mode"] == "background_wake" {
			out = append(out, c)
		}
	}
	return out
}

func (f *adminPushFixture) widgets(user int, device string, channels ...string) {
	f.t.Helper()
	if _, _, err := f.svc.SetWidgets(context.Background(), user, device, true, channels); err != nil {
		f.t.Fatalf("report widgets: %v", err)
	}
}

func (f *adminPushFixture) pending(serverDeviceID string) []string {
	f.t.Helper()
	var channels []string
	if err := f.pool.QueryRow(context.Background(), `SELECT wake_pending_channels FROM bloem_admin_push_devices WHERE server_device_id = $1`, serverDeviceID).Scan(&channels); err != nil {
		f.t.Fatal(err)
	}
	return channels
}

func (f *adminPushFixture) wake(channel string) {
	f.t.Helper()
	if err := f.svc.Wake(context.Background(), channel, 0); err != nil {
		f.t.Fatalf("wake %s: %v", channel, err)
	}
}

func (f *adminPushFixture) flush() int {
	f.t.Helper()
	return f.svc.FlushWakes(context.Background())
}

func TestAdminWidgetReportIsAccountScopedAndValidated(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	alice := f.account("alice", "admin", true)
	bob := f.account("bob", "admin", true)
	phone := f.register(alice, apnsRegistration("install-alice-1", 'a'))
	if phone.WidgetsPlaced || len(phone.WidgetChannels) != 0 {
		t.Fatalf("a new device has widgets: %+v", phone)
	}

	placed, channels, err := f.svc.SetWidgets(ctx, alice, phone.DeviceID, true, []string{"nodes", "sessions", "nodes"})
	if err != nil || !placed || strings.Join(channels, ",") != "nodes,sessions" {
		t.Fatalf("report = %v %v %v, want placed with sorted, de-duplicated channels", placed, channels, err)
	}
	for name, call := range map[string]func() error{
		"another account's device": func() error { _, _, e := f.svc.SetWidgets(ctx, bob, phone.DeviceID, true, []string{"nodes"}); return e },
		"an unknown device":        func() error { _, _, e := f.svc.SetWidgets(ctx, alice, "nope", true, []string{"nodes"}); return e },
	} {
		if err := call(); !errors.Is(err, ErrAdminPushNotFound) {
			t.Fatalf("%s: err = %v, want not found", name, err)
		}
	}
	for name, ch := range map[string][]string{"an unknown channel": {"traffic"}, "no channel while placed": {}, "revenue": {"revenue", "nodes"}} {
		if _, _, err := f.svc.SetWidgets(ctx, alice, phone.DeviceID, true, ch); !errors.Is(err, ErrAdminPushInvalid) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
	// Nothing the refused calls said was kept.
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("pending = %v", got)
	}
	var stored []string
	if err := f.pool.QueryRow(ctx, `SELECT widget_channels FROM bloem_admin_push_devices WHERE server_device_id = $1`, phone.DeviceID).Scan(&stored); err != nil || strings.Join(stored, ",") != "nodes,sessions" {
		t.Fatalf("channels after refused reports = %v (%v)", stored, err)
	}

	// Registering again keeps the report unless it carries one; reporting none clears it and any wake waiting.
	again := f.register(alice, apnsRegistration("install-alice-1", 'b'))
	if !again.WidgetsPlaced || strings.Join(again.WidgetChannels, ",") != "nodes,sessions" {
		t.Fatalf("re-registration lost the widget report: %+v", again)
	}
	f.wake("nodes")
	if got := f.pending(phone.DeviceID); strings.Join(got, ",") != "nodes" {
		t.Fatalf("pending = %v", got)
	}
	if placed, channels, err := f.svc.SetWidgets(ctx, alice, phone.DeviceID, false, []string{"nodes"}); err != nil || placed || len(channels) != 0 {
		t.Fatalf("removing the last widget = %v %v %v", placed, channels, err)
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("a wake stayed pending after the last widget went: %v", got)
	}

	// A registration can carry the report too, and false clears the channels.
	yes, no := true, false
	channelsIn := []string{"requests"}
	reg := apnsRegistration("install-alice-2", 'c')
	reg.WidgetsPlaced, reg.WidgetChannels = &yes, &channelsIn
	got := f.register(alice, reg)
	if !got.WidgetsPlaced || strings.Join(got.WidgetChannels, ",") != "requests" {
		t.Fatalf("registration with widgets = %+v", got)
	}
	reg.WidgetsPlaced = &no
	if got := f.register(alice, reg); got.WidgetsPlaced || len(got.WidgetChannels) != 0 {
		t.Fatalf("registration with widgets_placed false = %+v", got)
	}
	bad := []string{"traffic"}
	reg.WidgetsPlaced, reg.WidgetChannels = &yes, &bad
	if _, err := f.svc.Register(ctx, alice, reg); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("registration with an unknown channel: err = %v", err)
	}
}

func TestAdminWakeGoesOnlyToDevicesWithAWidgetOnThatChannel(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	demoted := f.account("demoted", "admin", true)
	sessions := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	nodes := f.register(admin, fcmRegistration("install-root-bbbb", 'b'))
	none := f.register(admin, fcmRegistration("install-root-cccc", 'c'))
	off := f.register(admin, fcmRegistration("install-root-dddd", 'd'))
	gone := f.register(demoted, apnsRegistration("install-demoted-1", 'e'))
	f.widgets(admin, sessions.DeviceID, "sessions")
	f.widgets(admin, nodes.DeviceID, "nodes")
	f.widgets(admin, off.DeviceID, "sessions")
	f.widgets(demoted, gone.DeviceID, "sessions")
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_admin_push_devices SET enabled = false WHERE server_device_id = $1`, off.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE users SET role = 'user' WHERE id = $1`, demoted); err != nil {
		t.Fatal(err)
	}

	f.wake("sessions")
	for device, want := range map[string]string{sessions.DeviceID: "sessions", nodes.DeviceID: "", none.DeviceID: "", off.DeviceID: "", gone.DeviceID: ""} {
		if got := strings.Join(f.pending(device), ","); got != want {
			t.Errorf("device %s pending = %q, want %q", device, got, want)
		}
	}
	if err := f.svc.Wake(ctx, "traffic", 0); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("a channel no widget shows: err = %v", err)
	}
}

func TestAdminRequestWakesReachOnlyAdminsOfTheRequestersOrganization(t *testing.T) {
	f := newAdminPushFixture(t)
	adminA := f.account("admin-a", "admin", true)
	adminB := f.account("admin-b", "admin", true)
	requester := f.account("req-a", "user", true)
	orgA, orgB := f.org(adminA, "org-a"), f.org(adminB, "org-b")
	f.member(orgA, adminA, "admin")
	f.member(orgA, requester, "user")
	f.member(orgB, adminB, "admin")
	a := f.register(adminA, apnsRegistration("install-admin-a1", 'a'))
	b := f.register(adminB, apnsRegistration("install-admin-b1", 'b'))
	f.widgets(adminA, a.DeviceID, "requests", "nodes")
	f.widgets(adminB, b.DeviceID, "requests", "nodes")

	if err := f.svc.Wake(context.Background(), "requests", requester); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(a.DeviceID); strings.Join(got, ",") != "requests" {
		t.Fatalf("the requester's admin: pending = %v", got)
	}
	if got := f.pending(b.DeviceID); len(got) != 0 {
		t.Fatalf("another organization's admin got a wake for a request that is not theirs: %v", got)
	}
	// A node describes the whole deployment: every admin hears it.
	f.wake("nodes")
	if got := f.pending(b.DeviceID); strings.Join(got, ",") != "nodes" {
		t.Fatalf("node wake for the other organization's admin: %v", got)
	}
}

func TestAdminWakeCoalescesChangesAndAppliesTheTenMinuteLimit(t *testing.T) {
	f := newAdminPushFixture(t)
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "sessions", "nodes", "libraries")
	start := f.now

	// 30 seconds: changes gather, and nothing goes out before the window has run.
	f.wake("sessions")
	if sent := f.flush(); sent != 0 {
		t.Fatalf("a wake went out with no coalescing window (%d)", sent)
	}
	f.now = start.Add(10 * time.Second)
	f.wake("nodes")
	f.now = start.Add(29 * time.Second)
	if sent := f.flush(); sent != 0 || len(f.relay.wakeCalls()) != 0 {
		t.Fatalf("a wake went out inside the 30 second window")
	}
	f.now = start.Add(31 * time.Second)
	if sent := f.flush(); sent != 1 {
		t.Fatalf("flushed %d wakes after the window, want 1", sent)
	}
	calls := f.relay.wakeCalls()
	if len(calls) != 1 {
		t.Fatalf("two changes in 30 seconds made %d wakes, want 1", len(calls))
	}
	id, _ := calls[0].Body["delivery_id"].(string)
	if !strings.HasPrefix(id, phone.DeviceID+".widgets-nodes-sessions-") {
		t.Fatalf("wake delivery id %q should carry the reason and both channels that changed", id)
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("pending after the send = %v", got)
	}

	// 10 minutes: a later change waits for the gap to pass, and the newest state wins.
	woke := f.now
	f.now = woke.Add(time.Minute)
	f.wake("libraries")
	f.now = woke.Add(2 * time.Minute)
	f.wake("sessions")
	f.now = woke.Add(9*time.Minute + 59*time.Second)
	if sent := f.flush(); sent != 0 {
		t.Fatalf("a second wake went out %v after the first", f.now.Sub(woke))
	}
	if got := strings.Join(f.pending(phone.DeviceID), ","); got != "libraries,sessions" {
		t.Fatalf("the changes held back were lost: %q", got)
	}
	f.now = woke.Add(10 * time.Minute)
	if sent := f.flush(); sent != 1 {
		t.Fatalf("flushed %d at ten minutes, want 1", sent)
	}
	calls = f.relay.wakeCalls()
	if len(calls) != 2 {
		t.Fatalf("wakes = %d, want 2 in total", len(calls))
	}
	if id, _ := calls[1].Body["delivery_id"].(string); !strings.Contains(id, ".widgets-libraries-sessions-") {
		t.Fatalf("the held-back wake %q should carry every channel that changed since the last", id)
	}
}

func TestAdminWakeNeverFollowsAVisiblePushWithinTwoMinutes(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "sessions", "nodes")
	start := f.now

	// A change that was waiting when a visible push went out is covered by that push, which refreshes the widgets.
	f.wake("sessions")
	f.now = start.Add(10 * time.Second)
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 && strings.Join(got, ",") != "sessions" {
		t.Fatalf("pending = %v", got)
	}
	f.now = start.Add(5 * time.Minute)
	f.flush()
	if got := len(f.relay.wakeCalls()); got != 0 {
		t.Fatalf("a wake went out for a change the visible push already covered (%d)", got)
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("the covered change stayed pending: %v", got)
	}

	// A change after the visible push waits until two minutes have passed since it.
	visible := start.Add(10 * time.Second)
	f.now = visible.Add(20 * time.Second)
	f.wake("nodes")
	f.now = visible.Add(119 * time.Second)
	if sent := f.flush(); sent != 0 {
		t.Fatalf("a wake went out %v after a visible push", f.now.Sub(visible))
	}
	if got := strings.Join(f.pending(phone.DeviceID), ","); got != "nodes" {
		t.Fatalf("the change after the push was dropped, pending = %q", got)
	}
	f.now = visible.Add(2*time.Minute + time.Second)
	if sent := f.flush(); sent != 1 {
		t.Fatalf("flushed %d just after the two minutes, want 1", sent)
	}
}

func TestAdminWakeIsSilentOnBothPlatformsAndCarriesNoText(t *testing.T) {
	f := newAdminPushFixture(t)
	admin := f.account("root", "admin", true)
	apple := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	android := f.register(admin, fcmRegistration("install-root-bbbb", 'b'))
	f.widgets(admin, apple.DeviceID, "sessions")
	f.widgets(admin, android.DeviceID, "sessions")
	f.wake("sessions")
	f.now = f.now.Add(31 * time.Second)
	if sent := f.flush(); sent != 2 {
		t.Fatalf("flushed %d, want one per device", sent)
	}
	calls := f.relay.wakeCalls()
	if len(calls) != 2 {
		t.Fatalf("wake calls = %d", len(calls))
	}
	collapse := calls[0].Body["collapse_id"]
	for _, call := range calls {
		for _, forbidden := range []string{"title", "body", "badge", "alert", "text"} {
			if _, has := call.Body[forbidden]; has {
				t.Fatalf("a wake carries %q: %v", forbidden, call.Body)
			}
		}
		if call.Body["mode"] != "background_wake" || call.IdempotencyKey == "" {
			t.Fatalf("wake call = %+v", call)
		}
		if call.Body["collapse_id"] != collapse || collapse == "" {
			t.Fatal("every wake should share one collapse id, so a newer wake replaces one still waiting")
		}
		switch call.Path {
		case relayAppleSendPath:
			if call.Body["topic"] != AdminPushAppleTopic {
				t.Fatalf("apple wake topic = %v", call.Body["topic"])
			}
		case relayFcmSendPath:
			if _, ok := call.Body["topic"]; ok {
				t.Fatal("fcm wake carries a topic")
			}
		default:
			t.Fatalf("path %q", call.Path)
		}
	}
	// A wake is not a notification: no delivery row, no attempt, and nothing to read.
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries`); got != 0 {
		t.Fatalf("a wake created %d delivery rows", got)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts`); got != 0 {
		t.Fatalf("a wake created %d attempt rows", got)
	}
}

func TestAdminWakeIsClaimedOnceAcrossReplicas(t *testing.T) {
	f := newAdminPushFixture(t)
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "sessions")
	other := f.newService(f.svc.sender, f.svc.settings, f.svc.cipher)

	// Both replicas see the same change.
	f.wake("sessions")
	if err := other.Wake(context.Background(), "sessions", 0); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(31 * time.Second)
	var wg sync.WaitGroup
	for _, svc := range []*AdminPushService{f.svc, other, f.svc, other} {
		wg.Add(1)
		go func() { defer wg.Done(); svc.FlushWakes(context.Background()) }()
	}
	wg.Wait()
	if got := len(f.relay.wakeCalls()); got != 1 {
		t.Fatalf("replicas sent %d wakes for one change, want 1", got)
	}
}

func TestAdminWakeFailureRequeuesOrSwitchesTheDeviceOff(t *testing.T) {
	f := newAdminPushFixture(t)
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "nodes")

	f.relay.answer(http.StatusServiceUnavailable, `{"error":{"code":"upstream_unavailable","message":"try later"}}`)
	f.wake("nodes")
	f.now = f.now.Add(31 * time.Second)
	f.flush()
	if got := strings.Join(f.pending(phone.DeviceID), ","); got != "nodes" {
		t.Fatalf("a wake the relay could not take was lost, pending = %q", got)
	}
	// The retry waits out the ten minute gap rather than hammering a relay that is down.
	failedAt := f.now
	f.now = failedAt.Add(5 * time.Minute)
	if sent := f.flush(); sent != 0 {
		t.Fatalf("retried after %v", f.now.Sub(failedAt))
	}
	f.relay.answer(http.StatusOK, `{"request_id":"relay-2","status":"accepted"}`)
	f.now = failedAt.Add(10 * time.Minute)
	if sent := f.flush(); sent != 1 {
		t.Fatalf("flushed %d at ten minutes, want the retry", sent)
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("pending after a good send = %v", got)
	}

	// A token Apple says is gone switches the device off, as for a visible push, and nothing is requeued.
	f.relay.answer(http.StatusUnprocessableEntity, `{"error":{"code":"apns_rejected","message":"APNs rejected the notification: BadDeviceToken"}}`)
	f.now = f.now.Add(11 * time.Minute)
	f.wake("nodes")
	f.now = f.now.Add(31 * time.Second)
	f.flush()
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1 AND NOT enabled`, phone.DeviceID); got != 1 {
		t.Fatal("a dead token left the device enabled")
	}
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("a wake for a dead token was requeued: %v", got)
	}
}

func TestAdminWakeFollowsTheOperatorsPushSwitchAndNeedsAWidget(t *testing.T) {
	f := newAdminPushFixture(t)
	admin := f.account("root", "admin", true)
	withWidget := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.register(admin, fcmRegistration("install-root-bbbb", 'b')) // no widget placed
	f.widgets(admin, withWidget.DeviceID, "sessions")
	f.wake("sessions")
	f.now = f.now.Add(31 * time.Second)
	f.flush()
	if got := len(f.relay.wakeCalls()); got != 1 {
		t.Fatalf("wakes = %d, want only the device with a widget", got)
	}

	// Push delivery switched off by the operator: nothing is marked and nothing is sent.
	f.svc.settings = NewSettings(mapSettingReader{
		SettingPushRelayURL: f.relay.server.URL, SettingPushRelayAPIKey: "relay-key",
		SettingApplePushDeliveryEnabled: "false", SettingAndroidPushDeliveryEnabled: "false",
	})
	f.now = f.now.Add(time.Hour)
	f.wake("sessions")
	if got := f.pending(withWidget.DeviceID); len(got) != 0 {
		t.Fatalf("a wake was marked with push delivery off: %v", got)
	}
	f.now = f.now.Add(time.Minute)
	if sent := f.flush(); sent != 0 {
		t.Fatalf("flushed %d with push delivery off", sent)
	}
}

func TestAdminHubEventsMapToWidgetChannels(t *testing.T) {
	for event, want := range map[string]string{
		"session.started": "sessions", "session.paused": "sessions", "session.resumed": "sessions", "session.stopped": "sessions",
		"session.progress": "", "session.updated": "",
	} {
		if got := hubWakeChannel(evt.ChannelSessions, event); got != want {
			t.Errorf("sessions/%s = %q, want %q", event, got, want)
		}
	}
	for event, want := range map[string]string{
		"scan.started": "libraries", "scan.completed": "libraries", "scan.failed": "libraries", "scan.cancelled": "libraries",
		"scan.progress": "", "scan.accepted": "",
	} {
		if got := hubWakeChannel(evt.ChannelScans, event); got != want {
			t.Errorf("scans/%s = %q, want %q", event, got, want)
		}
	}
	if got := hubWakeChannel(evt.ChannelScans, "session.started"); got != "" {
		t.Errorf("an event on the wrong channel woke %q", got)
	}
}

func TestAdminWatchEventsWakesWidgetsAndStillNotifiesAFailedScan(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, fcmRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "sessions", "libraries")
	if _, err := f.pool.Exec(ctx, `INSERT INTO media_folders (id, type, name) VALUES (91, 'movies', 'Movies')`); err != nil {
		t.Fatal(err)
	}
	hub := evt.NewHub("test", nil)
	done := make(chan struct{})
	go func() { defer close(done); f.svc.WatchEvents(ctx, hub) }()

	publish := func(channel evt.EventChannel, event string, run any) {
		t.Helper()
		if err := hub.PublishJSON(ctx, channel, event, run, evt.PublishOptions{AdminOnly: true}); err != nil {
			t.Fatal(err)
		}
	}
	// WatchEvents subscribes asynchronously, so keep publishing until it has heard one.
	waitFor := func(what string, publishAgain func(), done func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !done() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s (pending = %v)", what, f.pending(phone.DeviceID))
			}
			publishAgain()
			<-time.After(50 * time.Millisecond)
		}
	}
	pending := func() string { return strings.Join(f.pending(phone.DeviceID), ",") }

	// A session starting wakes the now-playing widget; a progress tick is not a change.
	waitFor("a session start to wake the sessions widget", func() {
		publish(evt.ChannelSessions, "session.progress", map[string]string{"id": "s1"})
		publish(evt.ChannelSessions, "session.started", map[string]string{"id": "s1"})
	}, func() bool { return pending() == "sessions" })
	// A scan finishing wakes the libraries widget too, and the two share one wake.
	waitFor("a scan end to wake the libraries widget", func() {
		publish(evt.ChannelScans, "scan.completed", evt.ScanRun{ID: "x", LibraryID: 91, Status: "completed"})
	}, func() bool { return pending() == "libraries,sessions" })
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries`); got != 0 {
		t.Fatalf("a scan that completed notified admins (%d deliveries)", got)
	}
	// A failed scan still notifies, as before the widgets: it is a visible push, which covers the wake.
	waitFor("a failed scan to notify admins", func() {
		publish(evt.ChannelScans, "scan.failed", evt.ScanRun{ID: "y", LibraryID: 91, Status: "failed", ErrorMessage: "mount lost"})
	}, func() bool {
		return f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindScanFailed) == 1
	})
	cancel()
	<-done
}

func TestAdminNodeWakesFollowOnlineAndOfflineChanges(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-aaaa", 'a'))
	f.widgets(admin, phone.DeviceID, "nodes")
	if _, err := f.pool.Exec(ctx, `INSERT INTO stream_nodes (id, name, type, url, enabled, healthy) VALUES
		(61, 'Encoder', 'transcode', 'https://node-a.example.test', true, true)`); err != nil {
		t.Fatal(err)
	}
	states, outages := newStateTracker(), newOutageTracker()
	set := func(healthy bool) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, `UPDATE stream_nodes SET healthy = $1 WHERE id = 61`, healthy); err != nil {
			t.Fatal(err)
		}
		f.svc.watchNodes(ctx, outages, states, f.now)
	}
	clearPending := func() {
		if _, err := f.pool.Exec(ctx, `UPDATE bloem_admin_push_devices SET wake_pending_channels = '{}', wake_pending_since = NULL`); err != nil {
			t.Fatal(err)
		}
	}
	set(true) // first sight: nothing changed
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("a node already up woke the widgets: %v", got)
	}
	set(false)
	if got := strings.Join(f.pending(phone.DeviceID), ","); got != "nodes" {
		t.Fatalf("a node going offline did not wake the nodes widget: %q", got)
	}
	clearPending()
	set(false) // still down: no change
	if got := f.pending(phone.DeviceID); len(got) != 0 {
		t.Fatalf("an unchanged node woke the widgets: %v", got)
	}
	set(true)
	if got := strings.Join(f.pending(phone.DeviceID), ","); got != "nodes" {
		t.Fatalf("a node coming back did not wake the nodes widget: %q", got)
	}
}

func TestAdminWakeStateTrackerReportsOnlyChanges(t *testing.T) {
	tr := newStateTracker()
	steps := []struct {
		up   bool
		want bool
	}{{true, false}, {true, false}, {false, true}, {false, false}, {true, true}}
	for i, s := range steps {
		if got := tr.changed("n", s.up); got != s.want {
			t.Fatalf("step %d: changed = %v, want %v", i, got, s.want)
		}
	}
	if tr.changed("other", false) {
		t.Fatal("a node first seen down is not a change")
	}
}

func TestAdminWakeIDCarriesTheReasonAndTheSortedChannels(t *testing.T) {
	at := time.Unix(1763254800, 0)
	got := adminWakeID([]string{"sessions", "nodes", "requests"}, at)
	if got != "widgets-nodes-requests-sessions-1763254800" {
		t.Fatalf("wake id = %q", got)
	}
	if !slices.Equal(AdminWidgetChannels(), []string{"sessions", "requests", "libraries", "nodes"}) {
		t.Fatalf("channels = %v", AdminWidgetChannels())
	}
	// The id must fit what the relay accepts after the device id is put in front of it.
	if wire := "01KXXXXXXXXXXXXXXXXXXXXXXX" + "." + adminWakeID(AdminWidgetChannels(), at); len(wire) > 128 {
		t.Fatalf("wake delivery id is %d bytes, the relay allows 128", len(wire))
	}
}
