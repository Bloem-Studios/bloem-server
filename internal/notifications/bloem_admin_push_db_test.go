package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Silo-Server/silo-server/internal/secret"
)

// relayCall is one request the fake relay received.
type relayCall struct {
	Path           string
	IdempotencyKey string
	Body           map[string]any
}

// fakeAdminRelay is a TLS relay whose answer a test can change.
type fakeAdminRelay struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []relayCall
	status int
	body   string
}

func newFakeAdminRelay(t *testing.T) *fakeAdminRelay {
	t.Helper()
	relay := &fakeAdminRelay{status: http.StatusOK, body: `{"request_id":"relay-1","status":"accepted"}`}
	relay.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := relayCall{Path: r.URL.Path, IdempotencyKey: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(raw, &call.Body)
		relay.mu.Lock()
		relay.calls = append(relay.calls, call)
		status, body := relay.status, relay.body
		relay.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(relay.server.Close)
	return relay
}

func (r *fakeAdminRelay) answer(status int, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status, r.body = status, body
}

func (r *fakeAdminRelay) received() []relayCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]relayCall(nil), r.calls...)
}

type adminPushFixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	svc   *AdminPushService
	relay *fakeAdminRelay
	now   time.Time
}

func newAdminPushFixture(t *testing.T) *adminPushFixture {
	t.Helper()
	pool := newMigratedTestPool(t)
	relay := newFakeAdminRelay(t)
	cipher, err := secret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	settings := NewSettings(mapSettingReader{SettingPushRelayURL: relay.server.URL, SettingPushRelayAPIKey: "relay-key"})
	sender := newPushSender(nil, nil, cipher, settings)
	sender.client = relay.server.Client()
	sender.developmentRelayURL = relay.server.URL
	f := &adminPushFixture{t: t, pool: pool, relay: relay, now: time.Now()}
	f.svc = f.newService(sender, settings, cipher)
	return f
}

func (f *adminPushFixture) newService(sender *pushSender, settings *Settings, cipher *secret.Cipher) *AdminPushService {
	return &AdminPushService{
		store: newAdminPushStore(f.pool), sender: sender, settings: settings, cipher: cipher,
		logger: slog.New(slog.DiscardHandler), now: func() time.Time { return f.now },
		wake: make(chan struct{}, 1),
	}
}

// account inserts a user with the role and returns its id.
func (f *adminPushFixture) account(name, role string, enabled bool) int {
	f.t.Helper()
	var id int
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO users (username, email, password_hash, role, enabled)
		VALUES ($1, $2, 'x', $3, $4) RETURNING id`, name, name+"@example.test", role, enabled).Scan(&id); err != nil {
		f.t.Fatalf("seed account %s: %v", name, err)
	}
	return id
}

func apnsRegistration(installation string, token byte) AdminPushRegistration {
	return AdminPushRegistration{
		InstallationID: installation, Platform: PushPlatformApple, Token: strings.Repeat(string(token), 64),
		APNsEnvironment: APNsEnvironmentSandbox, APNsTopic: AdminPushAppleTopic,
	}
}

func fcmRegistration(installation string, token byte) AdminPushRegistration {
	return AdminPushRegistration{InstallationID: installation, Platform: PushPlatformAndroid, Token: strings.Repeat(string(token), 64)}
}

func (f *adminPushFixture) register(userID int, reg AdminPushRegistration) *AdminPushRegistered {
	f.t.Helper()
	out, err := f.svc.Register(context.Background(), userID, reg)
	if err != nil {
		f.t.Fatalf("register: %v", err)
	}
	return out
}

func (f *adminPushFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestAdminPushRegistrationIsAccountScopedAndAdminTopicOnly(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	alice := f.account("alice", "admin", true)
	bob := f.account("bob", "admin", true)

	// Silo's own topic is not an admin topic, and a made-up one is refused too.
	for _, topic := range []string{ApplePushTopicSilo, "com.bloemstudios.hq", ""} {
		reg := apnsRegistration("install-alice-1", 'a')
		reg.APNsTopic = topic
		if _, err := f.svc.Register(ctx, alice, reg); !errors.Is(err, ErrAdminPushInvalid) {
			t.Fatalf("topic %q: err = %v, want ErrAdminPushInvalid", topic, err)
		}
	}
	if _, err := f.svc.Register(ctx, alice, AdminPushRegistration{InstallationID: "install-alice-1", Platform: "windows", Token: "x"}); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("unknown platform: err = %v", err)
	}
	if _, err := f.svc.Register(ctx, alice, AdminPushRegistration{InstallationID: "short", Platform: PushPlatformAndroid, Token: strings.Repeat("a", 64)}); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("short installation id: err = %v", err)
	}

	first := f.register(alice, apnsRegistration("install-alice-1", 'a'))
	again := f.register(alice, apnsRegistration("install-alice-1", 'b'))
	if first.DeviceID != again.DeviceID {
		t.Fatalf("re-registering the same installation changed the device id: %q -> %q", first.DeviceID, again.DeviceID)
	}
	if first.DisplayToken == again.DisplayToken {
		t.Fatal("re-registering must mint a new display token")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE user_id = $1`, alice); got != 1 {
		t.Fatalf("alice has %d device rows, want 1", got)
	}
	// The old token stops working once a registration replaces it.
	if _, err := f.svc.Content(ctx, first.DisplayToken, "any"); !errors.Is(err, ErrAdminPushUnauthorized) {
		t.Fatalf("old display token: err = %v, want unauthorized", err)
	}

	// Bob cannot switch kinds on, or remove, Alice's device.
	if _, err := f.svc.SetKinds(ctx, bob, again.DeviceID, map[string]bool{AdminKindScanFailed: false}); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("bob set kinds on alice's device: err = %v", err)
	}
	if err := f.svc.Unregister(ctx, bob, again.DeviceID); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("bob unregistered alice's device: err = %v", err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE user_id = $1`, alice); got != 1 {
		t.Fatal("alice's device was removed by another account")
	}

	// A phone that signs in as Bob takes its token over from Alice.
	f.register(bob, apnsRegistration("install-bob-0001", 'b'))
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE user_id = $1`, alice); got != 0 {
		t.Fatalf("alice kept %d device rows after her token moved to bob", got)
	}

	// Retire works from the display token alone, after sign-out.
	bobReg := f.register(bob, fcmRegistration("install-bob-0002", 'c'))
	if err := f.svc.Retire(ctx, "bad_wrong", bobReg.DeviceID); !errors.Is(err, ErrAdminPushUnauthorized) {
		t.Fatalf("retire with a wrong token: err = %v", err)
	}
	if err := f.svc.Retire(ctx, bobReg.DisplayToken, bobReg.DeviceID); err != nil {
		t.Fatalf("retire: %v", err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1`, bobReg.DeviceID); got != 0 {
		t.Fatal("retire left the device row")
	}
}

type adminKindCase struct {
	notice   AdminPushNotice
	kind     string
	title    string
	body     string
	lock     string
	page     string
	item     string
	actions  []string
	hasBadge bool
}

// adminKindCases is one case per server-emitted kind with the copy the spec gives.
func adminKindCases(admin int, at time.Time) []adminKindCase {
	return []adminKindCase{
		{RequestWaitingNotice("req-1", "sam", "Dune", admin, 3), AdminKindRequestWaiting, "sam requested Dune", "3 waiting", "New request waiting", "requests", "req-1", []string{"approve", "decline"}, true},
		{ScanFailedNotice("7", "Movies", "The scan did not finish", at, adminScanWindow), AdminKindScanFailed, "Scan failed: Movies", "The scan did not finish", "A library scan failed", "libraries", "7", nil, false},
		{BackupFailedNotice("run-1", "Home", "disk full"), AdminKindBackupFailed, "Backup failed on Home", "disk full", "A backup failed", "backups", "backups", nil, false},
		{NodeOfflineNotice("4", "Encoder", at), AdminKindNodeOffline, "Encoder is offline", "Since 09:30 UTC", "A node is offline", "nodes", "4", nil, false},
		{PluginFailedNotice("12", "Metadata", "crashed on start", at), AdminKindPluginFailed, "Metadata needs attention", "crashed on start", "A plugin needs attention", "plugins", "12", nil, false},
	}
}

func TestAdminPushEmitsEveryServerKindToAdminDevices(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	user := f.account("viewer", "user", true)
	apple := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	android := f.register(admin, fcmRegistration("install-root-droid", 'b'))
	// Admin-only: a viewer cannot register at all.
	if _, err := f.svc.Register(ctx, user, apnsRegistration("install-viewer-01", 'c')); !errors.Is(err, ErrAdminPushForbidden) {
		t.Fatalf("viewer registration: err = %v, want forbidden", err)
	}
	// An admin demoted after registering stops being a recipient.
	demoted := f.account("demoted", "admin", true)
	f.register(demoted, apnsRegistration("install-demoted-1", 'c'))
	if _, err := f.pool.Exec(ctx, `UPDATE users SET role = 'user' WHERE id = $1`, demoted); err != nil {
		t.Fatal(err)
	}

	at := time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)
	notices := adminKindCases(admin, at)
	for _, tc := range notices {
		if err := f.svc.Notify(ctx, tc.notice); err != nil {
			t.Fatalf("%s: notify: %v", tc.kind, err)
		}
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries`); got != len(notices) {
		t.Fatalf("deliveries = %d, want %d (one per kind, for the one admin)", got, len(notices))
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts`); got != 2*len(notices) {
		t.Fatalf("attempts = %d, want %d (admin's two devices, not the viewer's)", got, 2*len(notices))
	}
	if sent := f.svc.Drain(ctx); sent != 2*len(notices) {
		t.Fatalf("drained %d attempts, want %d", sent, 2*len(notices))
	}

	calls := f.relay.received()
	if len(calls) != 2*len(notices) {
		t.Fatalf("relay got %d calls, want %d", len(calls), 2*len(notices))
	}
	seenKeys := map[string]bool{}
	for _, call := range calls {
		if seenKeys[call.IdempotencyKey] {
			t.Fatalf("idempotency key %q was reused across attempts", call.IdempotencyKey)
		}
		seenKeys[call.IdempotencyKey] = true
		if call.Body["mode"] != "private_alert" {
			t.Fatalf("mode = %v, want private_alert", call.Body["mode"])
		}
		if _, leaked := call.Body["title"]; leaked {
			t.Fatal("notification text must never reach the relay")
		}
		wire, _ := call.Body["delivery_id"].(string)
		switch call.Path {
		case relayAppleSendPath:
			if !strings.HasPrefix(wire, apple.DeviceID+".") || call.Body["topic"] != AdminPushAppleTopic || call.Body["environment"] != "sandbox" {
				t.Fatalf("apple call = %v", call.Body)
			}
		case relayFcmSendPath:
			if !strings.HasPrefix(wire, android.DeviceID+".") {
				t.Fatalf("fcm delivery id %q lacks the device prefix", wire)
			}
			if _, ok := call.Body["topic"]; ok {
				t.Fatal("fcm sends carry no topic")
			}
		default:
			t.Fatalf("unexpected relay path %q", call.Path)
		}
		if collapse, _ := call.Body["collapse_id"].(string); collapse == "" || len(collapse) > 64 || collapse == wire {
			t.Fatalf("collapse id %q must be set, at most 64 bytes, and not the delivery id", collapse)
		}
	}

	assertKindContent(t, f, apple.DisplayToken, notices)
	// Only the Apple request call carries a badge; the relay has no badge for FCM.
	for _, call := range calls {
		if _, ok := call.Body["badge"]; ok && (call.Path != relayAppleSendPath || call.Body["badge"] != float64(3)) {
			t.Fatalf("badge sent where it should not be: %s %v", call.Path, call.Body)
		}
	}
}

// assertKindContent reads each kind's content back for the device it was sent to.
func assertKindContent(t *testing.T, f *adminPushFixture, displayToken string, notices []adminKindCase) {
	t.Helper()
	ctx := context.Background()
	for _, tc := range notices {
		var deliveryID string
		if err := f.pool.QueryRow(ctx, `SELECT id FROM bloem_admin_push_deliveries WHERE kind = $1`, tc.kind).Scan(&deliveryID); err != nil {
			t.Fatalf("%s: %v", tc.kind, err)
		}
		content, err := f.svc.Content(ctx, displayToken, deliveryID)
		if err != nil {
			t.Fatalf("%s: content: %v", tc.kind, err)
		}
		if content.Title != tc.title || content.Body != tc.body || content.LockScreen != tc.lock || content.Page != tc.page || content.ItemID != tc.item {
			t.Fatalf("%s: content = %+v", tc.kind, content)
		}
		if strings.Join(content.Actions, ",") != strings.Join(tc.actions, ",") {
			t.Fatalf("%s: actions = %v, want %v", tc.kind, content.Actions, tc.actions)
		}
		if (content.Badge != nil) != tc.hasBadge {
			t.Fatalf("%s: badge = %v", tc.kind, content.Badge)
		}
	}
}

func TestAdminPushDeliveryIDsAreIdempotentAcrossReplicas(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	f.register(admin, apnsRegistration("install-root-apple", 'a'))

	notice := RequestWaitingNotice("req-9", "sam", "Dune", admin, 1)
	other := f.newService(f.svc.sender, f.svc.settings, f.svc.cipher)

	// Two replicas and a retry all see the same event at once.
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(svc *AdminPushService) {
			defer wg.Done()
			if err := svc.Notify(ctx, notice); err != nil {
				t.Errorf("notify: %v", err)
			}
		}([]*AdminPushService{f.svc, other}[i%2])
	}
	wg.Wait()
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries`); got != 1 {
		t.Fatalf("deliveries = %d, want 1", got)
	}
	f.svc.Drain(ctx)
	other.Drain(ctx)
	if got := len(f.relay.received()); got != 1 {
		t.Fatalf("relay got %d sends for one event, want 1", got)
	}

	// A later occurrence of the same subject is a new delivery that collapses
	// onto the notification already showing.
	later := RequestWaitingNotice("req-9", "sam", "Dune", admin, 1)
	later.DedupeKey = "second"
	if err := f.svc.Notify(ctx, later); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	calls := f.relay.received()
	if len(calls) != 2 {
		t.Fatalf("relay calls = %d, want 2", len(calls))
	}
	if calls[0].Body["collapse_id"] != calls[1].Body["collapse_id"] || calls[0].Body["delivery_id"] == calls[1].Body["delivery_id"] {
		t.Fatalf("repeat occurrence should share a collapse id and not a delivery id: %v / %v", calls[0].Body, calls[1].Body)
	}
}

func TestAdminPushPerKindFilterAppliesAtQueueAndSendTime(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	tablet := f.register(admin, fcmRegistration("install-root-droid", 'b'))

	if _, err := f.svc.SetKinds(ctx, admin, phone.DeviceID, map[string]bool{"admin.nonsense": false}); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("unknown kind: err = %v", err)
	}
	states, err := f.svc.SetKinds(ctx, admin, phone.DeviceID, map[string]bool{AdminKindBackupFailed: false, AdminKindNodeOffline: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		wantOn := s.Kind != AdminKindBackupFailed && s.Kind != AdminKindNodeOffline
		if s.Enabled != wantOn {
			t.Fatalf("kind %s enabled = %v, want %v", s.Kind, s.Enabled, wantOn)
		}
	}
	// A switch applies to one device: the tablet still gets the backup.
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-2", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts a JOIN bloem_admin_push_devices d ON d.id = a.device_id WHERE d.server_device_id = $1`, phone.DeviceID); got != 0 {
		t.Fatalf("switched-off kind was queued for the phone (%d attempts)", got)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts a JOIN bloem_admin_push_devices d ON d.id = a.device_id WHERE d.server_device_id = $1`, tablet.DeviceID); got != 1 {
		t.Fatalf("tablet attempts = %d, want 1", got)
	}
	// Switching a kind back on takes effect for the next event.
	if _, err := f.svc.SetKinds(ctx, admin, phone.DeviceID, map[string]bool{AdminKindBackupFailed: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-3", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts a JOIN bloem_admin_push_devices d ON d.id = a.device_id WHERE d.server_device_id = $1`, phone.DeviceID); got != 1 {
		t.Fatalf("phone attempts after switching on = %d, want 1", got)
	}

	// A switch flipped after queueing still stops the send.
	if err := f.svc.Notify(ctx, NodeOfflineNotice("4", "Encoder", f.now)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetKinds(ctx, admin, tablet.DeviceID, map[string]bool{AdminKindNodeOffline: false}); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	for _, call := range f.relay.received() {
		if call.Path == relayFcmSendPath && strings.HasSuffix(fmt.Sprint(call.Body["delivery_id"]), adminDeliveryID(NodeOfflineNotice("4", "Encoder", f.now), admin)) {
			t.Fatal("a send went out for a kind the device had just switched off")
		}
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts WHERE upstream_reason = 'kind_disabled'`); got != 1 {
		t.Fatalf("kind_disabled attempts = %d, want 1", got)
	}
}

func TestAdminPushContentIsScopedToTheDeviceAndAdminStanding(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	alice := f.account("alice", "admin", true)
	bob := f.account("bob", "admin", true)
	aliceDev := f.register(alice, apnsRegistration("install-alice-1", 'a'))
	aliceTablet := f.register(alice, fcmRegistration("install-alice-2", 'b'))
	bobDev := f.register(bob, apnsRegistration("install-bob-0001", 'c'))

	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "disk full")); err != nil {
		t.Fatal(err)
	}
	var aliceDelivery, bobDelivery string
	if err := f.pool.QueryRow(ctx, `SELECT id FROM bloem_admin_push_deliveries WHERE user_id = $1`, alice).Scan(&aliceDelivery); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT id FROM bloem_admin_push_deliveries WHERE user_id = $1`, bob).Scan(&bobDelivery); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Content(ctx, aliceDev.DisplayToken, aliceDelivery); err != nil {
		t.Fatalf("alice's own delivery: %v", err)
	}
	if _, err := f.svc.Content(ctx, aliceTablet.DisplayToken, aliceDelivery); err != nil {
		t.Fatalf("alice's other device reads the same delivery it was sent: %v", err)
	}
	for name, token := range map[string]string{"alice": aliceDev.DisplayToken, "bob": bobDev.DisplayToken} {
		other := bobDelivery
		if name == "bob" {
			other = aliceDelivery
		}
		if _, err := f.svc.Content(ctx, token, other); !errors.Is(err, ErrAdminPushNotFound) {
			t.Fatalf("%s read another account's delivery: err = %v", name, err)
		}
	}
	if _, err := f.svc.Content(ctx, "", aliceDelivery); !errors.Is(err, ErrAdminPushUnauthorized) {
		t.Fatalf("no token: err = %v", err)
	}
	if _, err := f.svc.Content(ctx, "bad_unknown", aliceDelivery); !errors.Is(err, ErrAdminPushUnauthorized) {
		t.Fatalf("unknown token: err = %v", err)
	}
	if _, err := f.svc.Content(ctx, "Bearer "+aliceDev.DisplayToken, aliceDelivery); !errors.Is(err, ErrAdminPushUnauthorized) {
		t.Fatalf("malformed token: err = %v", err)
	}

	// Expired text is gone.
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_admin_push_deliveries SET expires_at = now() - interval '1 minute' WHERE id = $1`, aliceDelivery); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Content(ctx, aliceDev.DisplayToken, aliceDelivery); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("expired delivery: err = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_admin_push_deliveries SET expires_at = now() + interval '1 hour' WHERE id = $1`, aliceDelivery); err != nil {
		t.Fatal(err)
	}

	// Admin-only is judged on every read: a demoted or disabled account loses it.
	for _, change := range []string{`role = 'user'`, `enabled = false`} {
		if _, err := f.pool.Exec(ctx, `UPDATE users SET `+change+` WHERE id = $1`, alice); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Content(ctx, aliceDev.DisplayToken, aliceDelivery); !errors.Is(err, ErrAdminPushForbidden) {
			t.Fatalf("%s: err = %v, want forbidden", change, err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE users SET role = 'admin', enabled = true WHERE id = $1`, alice); err != nil {
			t.Fatal(err)
		}
	}
	// And queued sends to a demoted admin stop.
	if _, err := f.pool.Exec(ctx, `UPDATE users SET role = 'user' WHERE id = $1`, alice); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts WHERE upstream_reason = 'device_unavailable'`); got != 2 {
		t.Fatalf("demoted admin's attempts stopped = %d, want 2", got)
	}
}

func TestAdminPushRequestAudienceFollowsTheRequesterOrganization(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	adminA := f.account("admin-a", "admin", true)
	adminB := f.account("admin-b", "admin", true)
	requester := f.account("req-a", "user", true)
	orgA := f.org(adminA, "org-a")
	orgB := f.org(adminB, "org-b")
	f.member(orgA, adminA, "admin")
	f.member(orgA, requester, "user")
	f.member(orgB, adminB, "admin")
	f.register(adminA, apnsRegistration("install-admin-a1", 'a'))
	f.register(adminB, apnsRegistration("install-admin-b1", 'b'))

	if err := f.svc.Notify(ctx, RequestWaitingNotice("req-1", "sam", "Dune", requester, 1)); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE user_id = $1`, adminA); got != 1 {
		t.Fatalf("admin in the requester's organization got %d deliveries, want 1", got)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE user_id = $1`, adminB); got != 0 {
		t.Fatalf("admin of another organization got %d deliveries, want 0", got)
	}
	// Infrastructure state is deployment-wide: both admins hear about it.
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed); got != 2 {
		t.Fatalf("backup deliveries = %d, want 2", got)
	}
}

func (f *adminPushFixture) org(owner int, slug string) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `INSERT INTO organizations(slug, name, status, owner_account_id)
		VALUES ($1, $1, 'active', $2) RETURNING id`, slug, owner).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f *adminPushFixture) member(org string, account int, role string) {
	f.t.Helper()
	ctx := context.Background()
	// A fixture trigger may already have placed the account in the default org.
	if _, err := f.pool.Exec(ctx, `DELETE FROM organization_memberships WHERE account_id = $1`, account); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO organization_memberships(organization_id, account_id, status, legacy_role)
		VALUES ($1, $2, 'active', $3)`, org, account, role); err != nil {
		f.t.Fatalf("membership: %v", err)
	}
}

func TestAdminPushSenderRetriesTransientFailuresAndDisablesDeadTokens(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	phone := f.register(admin, apnsRegistration("install-root-apple", 'a'))

	f.relay.answer(http.StatusServiceUnavailable, `{"error":{"code":"upstream_unavailable","message":"try later"}}`)
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	var outcome string
	var next *time.Time
	var number int
	if err := f.pool.QueryRow(ctx, `SELECT outcome, next_retry_at, attempt_number FROM bloem_admin_push_attempts`).Scan(&outcome, &next, &number); err != nil {
		t.Fatal(err)
	}
	if outcome != PushOutcomeRetrying || next == nil || number != 1 {
		t.Fatalf("after a 503: outcome=%s next=%v attempt=%d, want retrying with a retry time", outcome, next, number)
	}
	// Not due yet: nothing is sent again.
	before := len(f.relay.received())
	f.svc.Drain(ctx)
	if len(f.relay.received()) != before {
		t.Fatal("a retry ran before it was due")
	}
	// Due: it is resent with the same idempotency key, and succeeds.
	firstKey := f.relay.received()[0].IdempotencyKey
	f.relay.answer(http.StatusOK, `{"request_id":"relay-2","status":"accepted"}`)
	if _, err := f.pool.Exec(ctx, `UPDATE bloem_admin_push_attempts SET next_retry_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	calls := f.relay.received()
	if calls[len(calls)-1].IdempotencyKey != firstKey {
		t.Fatalf("the retry used key %q, want the original %q", calls[len(calls)-1].IdempotencyKey, firstKey)
	}
	if err := f.pool.QueryRow(ctx, `SELECT outcome FROM bloem_admin_push_attempts`).Scan(&outcome); err != nil || outcome != PushOutcomeDelivered {
		t.Fatalf("outcome = %s (%v), want delivered", outcome, err)
	}

	// A token Apple says is gone switches the device off until it registers again.
	f.relay.answer(http.StatusUnprocessableEntity, `{"error":{"code":"apns_rejected","message":"APNs rejected the notification: BadDeviceToken"}}`)
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-2", "Home", "")); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1 AND NOT enabled`, phone.DeviceID); got != 1 {
		t.Fatal("a dead token left the device enabled")
	}
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-3", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed); got != 2 {
		t.Fatalf("a disabled device still got queued deliveries (%d total, want 2)", got)
	}
	f.register(admin, apnsRegistration("install-root-apple", 'd'))
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1 AND enabled`, phone.DeviceID); got != 1 {
		t.Fatal("registering again did not re-enable the device")
	}
}

func TestAdminPushRespectsTheOperatorsDeliverySwitch(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	settings := NewSettings(mapSettingReader{
		SettingPushRelayURL: f.relay.server.URL, SettingPushRelayAPIKey: "relay-key",
		SettingApplePushDeliveryEnabled: "false", SettingAndroidPushDeliveryEnabled: "true",
	})
	f.svc.settings = settings
	if _, err := f.svc.Register(ctx, admin, apnsRegistration("install-root-apple", 'a')); !errors.Is(err, ErrAdminPushUnavailable) {
		t.Fatalf("apple registration with apple push off: err = %v", err)
	}
	f.register(admin, fcmRegistration("install-root-droid", 'b'))
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	f.svc.Drain(ctx)
	if len(f.relay.received()) != 1 {
		t.Fatalf("relay calls = %d, want 1 (android only)", len(f.relay.received()))
	}
}
