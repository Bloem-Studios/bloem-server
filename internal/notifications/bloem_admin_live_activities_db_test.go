package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/config"
	"github.com/Silo-Server/silo-server/internal/secret"
)

// An activity push token is hex, as a device token is, and longer.
var (
	liveTokenA = strings.Repeat("a1", 80)
	liveTokenB = strings.Repeat("b2", 80)
	liveStart  = strings.Repeat("c3", 80)
)

// newLiveFixture is the admin push fixture with the operator's Live Activities switch on.
func newLiveFixture(t *testing.T) *adminPushFixture {
	t.Helper()
	f := newAdminPushFixture(t)
	f.svc.settings = NewSettings(mapSettingReader{
		SettingPushRelayURL: f.relay.server.URL, SettingPushRelayAPIKey: "relay-key",
		config.AdminLiveActivitiesSettingKey: "true",
	})
	return f
}

func iosActivity(id, kind, subject, token string) LiveActivityRegistration {
	return LiveActivityRegistration{ActivityID: id, Kind: kind, SubjectID: subject, Platform: LivePlatformIOS, PushToken: token, Environment: APNsEnvironmentSandbox}
}

func (f *adminPushFixture) registerLive(userID int, deviceID string, in LiveActivityRegistration) {
	f.t.Helper()
	if err := f.svc.RegisterLiveActivity(context.Background(), userID, deviceID, in); err != nil {
		f.t.Fatalf("register live activity %s: %v", in.ActivityID, err)
	}
}

func (f *adminPushFixture) liveToken(activityID string) (ciphertext string, null bool) {
	f.t.Helper()
	var stored *string
	if err := f.pool.QueryRow(context.Background(), `SELECT token_ciphertext FROM bloem_admin_live_activities WHERE activity_id = $1`, activityID).Scan(&stored); err != nil {
		f.t.Fatalf("read activity %s: %v", activityID, err)
	}
	if stored == nil {
		return "", true
	}
	return *stored, false
}

func testCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	cipher, err := secret.New([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

const (
	activityOne   = "6f1c2b9e-1d4a-4c7e-9a2b-000000000001"
	activityTwo   = "6f1c2b9e-1d4a-4c7e-9a2b-000000000002"
	activityThree = "6f1c2b9e-1d4a-4c7e-9a2b-000000000003"
)

func TestRegisterLiveActivityStoresTheTokenEncrypted(t *testing.T) {
	f := newLiveFixture(t)
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))

	stored, null := f.liveToken(activityOne)
	if null || stored == "" {
		t.Fatal("an iOS activity must keep its token")
	}
	if stored == liveTokenA || strings.Contains(stored, liveTokenA) {
		t.Fatal("the activity token is stored in the clear")
	}
	plain, err := testCipher(t).Decrypt(stored, adminLiveTokenAAD(activityOne))
	if err != nil || plain != liveTokenA {
		t.Fatalf("decrypt = %q, %v; want the registered token", plain, err)
	}
	var kind, subject, platform, environment string
	var expires, created time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT kind, subject_id, platform, environment, created_at, expires_at
		FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne).Scan(&kind, &subject, &platform, &environment, &created, &expires); err != nil {
		t.Fatal(err)
	}
	if kind != LiveKindScan || subject != "7" || platform != LivePlatformIOS || environment != APNsEnvironmentSandbox {
		t.Fatalf("stored %s %s %s %s", kind, subject, platform, environment)
	}
	if got := expires.Sub(created); got != adminLiveLifetime {
		t.Fatalf("expires %v after creation, want %v", got, adminLiveLifetime)
	}
}

func TestRegisterLiveActivityOnlyOnTheCallersDevice(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	alice := f.account("alice", "admin", true)
	bob := f.account("bob", "admin", true)
	device := f.register(alice, apnsRegistration("install-alice-1", 'a'))

	if err := f.svc.RegisterLiveActivity(ctx, bob, device.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenA)); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("bob registered on alice's device: err = %v, want not found", err)
	}
	if err := f.svc.RegisterLiveActivity(ctx, alice, "no-such-device", iosActivity(activityOne, LiveKindBackup, "", liveTokenA)); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("unknown device: err = %v, want not found", err)
	}
	start := liveStart
	if err := f.svc.SetLiveStart(ctx, bob, device.DeviceID, &start, []string{LiveKindScan}); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("bob set alice's start token: err = %v, want not found", err)
	}
	f.registerLive(alice, device.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenA))
	if err := f.svc.EndLiveActivity(ctx, bob, device.DeviceID, activityOne); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("bob ended alice's activity: err = %v, want not found", err)
	}
	// Another account's device cannot take the activity id over either.
	bobDevice := f.register(bob, apnsRegistration("install-bob-0001", 'b'))
	if err := f.svc.RegisterLiveActivity(ctx, bob, bobDevice.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenB)); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("bob reused alice's activity id: err = %v, want invalid", err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities a JOIN bloem_admin_push_devices d ON d.id = a.device_id WHERE d.user_id = $1`, alice); got != 1 {
		t.Fatalf("alice has %d activities, want 1", got)
	}
	stored, _ := f.liveToken(activityOne)
	if plain, _ := testCipher(t).Decrypt(stored, adminLiveTokenAAD(activityOne)); plain != liveTokenA {
		t.Fatal("another account replaced alice's activity token")
	}
}

func TestRegisterLiveActivityReplacesTheTokenForTheSameID(t *testing.T) {
	f := newLiveFixture(t)
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	created := f.count(`SELECT extract(epoch FROM expires_at)::int FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne)
	if _, err := f.pool.Exec(context.Background(), `UPDATE bloem_admin_live_activities SET last_timestamp = 42, last_state_hash = 'h' WHERE activity_id = $1`, activityOne); err != nil {
		t.Fatal(err)
	}

	f.now = f.now.Add(10 * time.Minute)
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenB))
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != 1 {
		t.Fatalf("rows = %d, want 1: a repeated id replaces the token", got)
	}
	stored, _ := f.liveToken(activityOne)
	if plain, _ := testCipher(t).Decrypt(stored, adminLiveTokenAAD(activityOne)); plain != liveTokenB {
		t.Fatal("the rotated token was not stored")
	}
	// The activity keeps its lifetime and its timestamp, so updates keep increasing; the new token
	// has been sent nothing yet, so the last state is forgotten.
	if got := f.count(`SELECT extract(epoch FROM expires_at)::int FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne); got != created {
		t.Fatal("token rotation moved the activity's expiry")
	}
	if got := f.count(`SELECT last_timestamp::int FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne); got != 42 {
		t.Fatalf("last_timestamp = %d, want it kept", got)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1 AND last_state_hash IS NULL`, activityOne); got != 1 {
		t.Fatal("token rotation must forget the last state sent")
	}
}

func TestSixthActivityReplacesTheOldest(t *testing.T) {
	f := newLiveFixture(t)
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	ids := []string{}
	for i := range 6 {
		id := "6f1c2b9e-1d4a-4c7e-9a2b-00000000010" + string(rune('0'+i))
		ids = append(ids, id)
		f.registerLive(admin, device.DeviceID, iosActivity(id, LiveKindBackup, "", liveTokenA))
		f.now = f.now.Add(time.Minute)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != adminLiveMaxPerDevice {
		t.Fatalf("activities = %d, want %d", got, adminLiveMaxPerDevice)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1`, ids[0]); got != 0 {
		t.Fatal("the sixth activity must replace the oldest")
	}
	// Another device's activities are not counted against this one.
	other := f.register(admin, apnsRegistration("install-root-ipad", 'b'))
	f.registerLive(admin, other.DeviceID, iosActivity(activityThree, LiveKindBackup, "", liveTokenB))
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != adminLiveMaxPerDevice+1 {
		t.Fatalf("activities = %d, want %d", got, adminLiveMaxPerDevice+1)
	}
}

func TestAndroidActivityHasNoToken(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	android := f.register(admin, fcmRegistration("install-root-droid", 'b'))
	apple := f.register(admin, apnsRegistration("install-root-apple", 'a'))

	f.registerLive(admin, android.DeviceID, LiveActivityRegistration{ActivityID: activityOne, Kind: LiveKindScan, SubjectID: "3", Platform: LivePlatformAndroid})
	if _, null := f.liveToken(activityOne); !null {
		t.Fatal("an Android activity must have no token")
	}
	for name, in := range map[string]LiveActivityRegistration{
		"android with a token":     {ActivityID: activityTwo, Kind: LiveKindBackup, Platform: LivePlatformAndroid, PushToken: liveTokenA},
		"android with environment": {ActivityID: activityTwo, Kind: LiveKindBackup, Platform: LivePlatformAndroid, Environment: APNsEnvironmentSandbox},
		"ios activity on android":  iosActivity(activityTwo, LiveKindBackup, "", liveTokenA),
	} {
		if err := f.svc.RegisterLiveActivity(ctx, admin, android.DeviceID, in); !errors.Is(err, ErrAdminPushInvalid) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
	if err := f.svc.RegisterLiveActivity(ctx, admin, apple.DeviceID, LiveActivityRegistration{ActivityID: activityTwo, Kind: LiveKindBackup, Platform: LivePlatformAndroid}); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("android activity on an Apple device: err = %v, want invalid", err)
	}
}

func TestRegisterLiveActivityRefusesBadInput(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	for name, in := range map[string]LiveActivityRegistration{
		"activity id not a uuid":   iosActivity("activity-1", LiveKindBackup, "", liveTokenA),
		"unknown kind":             iosActivity(activityOne, "shift", "", liveTokenA),
		"scan without a library":   iosActivity(activityOne, LiveKindScan, "", liveTokenA),
		"scan with a library name": iosActivity(activityOne, LiveKindScan, "Music videos", liveTokenA),
		"backup with a subject":    iosActivity(activityOne, LiveKindBackup, "7", liveTokenA),
		"ios without a token":      iosActivity(activityOne, LiveKindBackup, "", ""),
		"ios token not hex":        iosActivity(activityOne, LiveKindBackup, "", strings.Repeat("zz", 40)),
		"ios token too short":      iosActivity(activityOne, LiveKindBackup, "", strings.Repeat("a", 63)),
		"ios token too long":       iosActivity(activityOne, LiveKindBackup, "", strings.Repeat("a", 201)),
		"ios without environment":  {ActivityID: activityOne, Kind: LiveKindBackup, Platform: LivePlatformIOS, PushToken: liveTokenA},
		"unknown platform":         {ActivityID: activityOne, Kind: LiveKindBackup, Platform: "apple", PushToken: liveTokenA, Environment: APNsEnvironmentSandbox},
	} {
		if err := f.svc.RegisterLiveActivity(ctx, admin, device.DeviceID, in); !errors.Is(err, ErrAdminPushInvalid) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != 0 {
		t.Fatalf("a refused registration stored %d rows", got)
	}
	// The id is stored as the canonical UUID, whatever case the app sent.
	f.registerLive(admin, device.DeviceID, iosActivity(strings.ToUpper(activityOne), LiveKindBackup, "", strings.ToUpper(liveTokenA)))
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne); got != 1 {
		t.Fatal("the activity id was not stored as a canonical UUID")
	}
}

func TestLiveActivitiesNeedTheOperatorSwitch(t *testing.T) {
	f := newAdminPushFixture(t) // the switch is off unless an operator turns it on
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	if f.svc.LiveActivitiesEnabled(ctx) {
		t.Fatal("Live Activities must default to off")
	}
	if err := f.svc.RegisterLiveActivity(ctx, admin, device.DeviceID, iosActivity(activityOne, LiveKindBackup, "", liveTokenA)); !errors.Is(err, ErrAdminPushUnavailable) {
		t.Fatalf("register with the switch off: err = %v, want unavailable", err)
	}
	start := liveStart
	if err := f.svc.SetLiveStart(ctx, admin, device.DeviceID, &start, []string{LiveKindScan}); !errors.Is(err, ErrAdminPushUnavailable) {
		t.Fatalf("start token with the switch off: err = %v, want unavailable", err)
	}
	// Ending and clearing always work, so an app can clean up after the switch goes off.
	if err := f.svc.EndLiveActivity(ctx, admin, device.DeviceID, activityOne); err != nil {
		t.Fatalf("end with the switch off: %v", err)
	}
	if err := f.svc.SetLiveStart(ctx, admin, device.DeviceID, nil, nil); err != nil {
		t.Fatalf("clearing the start token with the switch off: %v", err)
	}
}

func TestEndLiveActivityDeletes(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	f.registerLive(admin, device.DeviceID, iosActivity(activityTwo, LiveKindBackup, "", liveTokenB))
	if err := f.svc.EndLiveActivity(ctx, admin, device.DeviceID, strings.ToUpper(activityOne)); err != nil {
		t.Fatalf("end: %v", err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne); got != 0 {
		t.Fatal("ending an activity must delete its row and token")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != 1 {
		t.Fatal("ending one activity removed another")
	}
	// Ending again, or an activity the server never had, is not an error.
	if err := f.svc.EndLiveActivity(ctx, admin, device.DeviceID, activityOne); err != nil {
		t.Fatalf("end twice: %v", err)
	}
	if err := f.svc.EndLiveActivity(ctx, admin, device.DeviceID, "not-a-uuid"); !errors.Is(err, ErrAdminPushInvalid) {
		t.Fatalf("end with a bad id: err = %v, want invalid", err)
	}
}

func TestSetLiveStartStoresKindsAndClears(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	android := f.register(admin, fcmRegistration("install-root-droid", 'b'))
	readStart := func() (string, []string) {
		t.Helper()
		var stored *string
		var kinds []string
		if err := f.pool.QueryRow(ctx, `SELECT live_start_token_ciphertext, live_start_kinds FROM bloem_admin_push_devices WHERE server_device_id = $1`,
			device.DeviceID).Scan(&stored, &kinds); err != nil {
			t.Fatal(err)
		}
		if stored == nil {
			return "", kinds
		}
		return *stored, kinds
	}

	start := liveStart
	if err := f.svc.SetLiveStart(ctx, admin, device.DeviceID, &start, []string{LiveKindBackup, LiveKindScan, LiveKindScan}); err != nil {
		t.Fatalf("set start: %v", err)
	}
	stored, kinds := readStart()
	if stored == "" || strings.Contains(stored, liveStart) {
		t.Fatal("the start token must be stored, encrypted")
	}
	var deviceRowID string
	if err := f.pool.QueryRow(ctx, `SELECT id FROM bloem_admin_push_devices WHERE server_device_id = $1`, device.DeviceID).Scan(&deviceRowID); err != nil {
		t.Fatal(err)
	}
	if plain, err := testCipher(t).Decrypt(stored, adminLiveStartTokenAAD(deviceRowID)); err != nil || plain != liveStart {
		t.Fatalf("decrypt start token = %q, %v", plain, err)
	}
	if !slices.Equal(kinds, []string{LiveKindScan, LiveKindBackup}) {
		t.Fatalf("kinds = %v, want [scan backup]", kinds)
	}

	for name, call := range map[string]func() error{
		"unknown kind":       func() error { return f.svc.SetLiveStart(ctx, admin, device.DeviceID, &start, []string{"shift"}) },
		"token not hex":      func() error { bad := "not hex"; return f.svc.SetLiveStart(ctx, admin, device.DeviceID, &bad, nil) },
		"android has none":   func() error { return f.svc.SetLiveStart(ctx, admin, android.DeviceID, &start, []string{LiveKindScan}) },
		"kinds and no token": func() error { return f.svc.SetLiveStart(ctx, admin, device.DeviceID, nil, []string{LiveKindScan}) },
	} {
		if err := call(); !errors.Is(err, ErrAdminPushInvalid) {
			t.Fatalf("%s: err = %v, want invalid", name, err)
		}
	}

	if err := f.svc.SetLiveStart(ctx, admin, device.DeviceID, nil, nil); err != nil {
		t.Fatalf("clear start: %v", err)
	}
	if stored, kinds = readStart(); stored != "" || len(kinds) != 0 {
		t.Fatalf("cleared start kept token=%t kinds=%v", stored != "", kinds)
	}
}

func TestDeviceDeleteRemovesItsActivities(t *testing.T) {
	f := newLiveFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	kept := f.register(admin, apnsRegistration("install-root-ipad", 'b'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	f.registerLive(admin, kept.DeviceID, iosActivity(activityTwo, LiveKindBackup, "", liveTokenB))
	if err := f.svc.Unregister(ctx, admin, device.DeviceID); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1`, activityOne); got != 0 {
		t.Fatal("removing a device must remove its activities")
	}
	// Retiring on the display token (sign-out) removes them too.
	if err := f.svc.Retire(ctx, kept.DisplayToken, kept.DeviceID); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_live_activities`); got != 0 {
		t.Fatal("retiring a device must remove its activities")
	}
}

// ---- S1-2: the relay request -------------------------------------------------

// liveRelay answers each relay call with what answer returns, so a test can make
// Apple and FCM sends answer differently, and add a Retry-After.
type liveRelay struct {
	mu     sync.Mutex
	calls  []relayCall
	answer func(relayCall) (status int, body, retryAfter string)
}

func (l *liveRelay) received() []relayCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]relayCall(nil), l.calls...)
}

func (l *liveRelay) answerAll(status int, body, retryAfter string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answer = func(relayCall) (int, string, string) { return status, body, retryAfter }
}

const relayAccepted = `{"request_id":"relay-1","status":"accepted"}`

func newLiveRelayFixture(t *testing.T) (*adminPushFixture, *liveRelay) {
	t.Helper()
	f := newLiveFixture(t)
	lr := &liveRelay{}
	lr.answerAll(http.StatusOK, relayAccepted, "")
	f.relay.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		call := relayCall{Path: r.URL.Path, IdempotencyKey: r.Header.Get("Idempotency-Key")}
		_ = json.Unmarshal(raw, &call.Body)
		lr.mu.Lock()
		lr.calls = append(lr.calls, call)
		answer := lr.answer
		lr.mu.Unlock()
		status, body, retryAfter := answer(call)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return f, lr
}

func runningScan(library string, at time.Time) adminJobState {
	progress, processed, total := 0.42, 120, 300
	return adminJobState{Phase: LivePhaseRunning, Progress: &progress, Processed: &processed, Total: &total, LibraryID: library, UpdatedAt: at.Unix()}
}

func (f *adminPushFixture) queue(kind, subject, event string, state adminJobState) {
	f.t.Helper()
	if err := f.svc.queueLive(context.Background(), liveUpdate{Kind: kind, SubjectID: subject, Event: event, State: state}); err != nil {
		f.t.Fatalf("queue %s %s: %v", kind, event, err)
	}
}

func (f *adminPushFixture) liveCount(activityID string) int {
	f.t.Helper()
	return f.count(`SELECT count(*) FROM bloem_admin_live_activities WHERE activity_id = $1`, activityID)
}

// liveStringIsAnID is the server's own rule for a string in a content state or in
// attributes: a phase or kind word, a digit id, a device id or empty.
func liveStringIsAnID(value, deviceID string) bool {
	switch value {
	case "", LivePhaseRunning, LivePhaseFinished, LivePhaseFailed, LiveKindScan, LiveKindBackup, deviceID:
		return true
	}
	return libraryIDPattern.MatchString(value)
}

func assertOnlyIDs(t *testing.T, field string, value any, deviceID string) {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a flat object", field, value)
	}
	for key, member := range object {
		switch v := member.(type) {
		case string:
			if !liveStringIsAnID(v, deviceID) {
				t.Fatalf("%s.%s = %q, which is not an id, a state word or empty", field, key, v)
			}
		case float64, bool, nil:
		default:
			t.Fatalf("%s.%s = %#v, want a scalar", field, key, member)
		}
	}
}

func TestLiveRequestCarriesOnlyIdsCountsAndTimes(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	f.registerLive(admin, device.DeviceID, iosActivity(activityTwo, LiveKindBackup, "", liveTokenB))

	f.queue(LiveKindScan, "7", LiveEventUpdate, runningScan("7", f.now))
	progress := 0.5
	f.queue(LiveKindBackup, "", LiveEventUpdate, adminJobState{Phase: LivePhaseRunning, Progress: &progress, UpdatedAt: f.now.Unix()})
	if sent := f.svc.FlushLive(ctx); sent != 2 {
		t.Fatalf("flushed %d, want 2", sent)
	}
	calls := relay.received()
	if len(calls) != 2 {
		t.Fatalf("relay calls = %d, want 2", len(calls))
	}
	for _, call := range calls {
		assertOnlyIDs(t, "content_state", call.Body["content_state"], device.DeviceID)
		for key, value := range call.Body {
			if s, ok := value.(string); ok && key != "token" && key != "delivery_id" && key != "topic" && strings.ContainsAny(s, " @:/") {
				t.Fatalf("%s = %q carries text", key, s)
			}
		}
	}

	// A push-started activity's attributes are ids, a kind word and a time.
	var stored AdminPushDevice
	got, _, err := f.svc.store.deviceByID(ctx, f.deviceRowID(device.DeviceID))
	if err != nil || got == nil {
		t.Fatalf("load device: %v", err)
	}
	stored = *got
	_, body, err := adminLiveStartPayload(&stored, liveStart, LiveKindScan, runningScan("7", f.now), f.now.Add(-time.Minute), f.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	assertOnlyIDs(t, "attributes", decoded["attributes"], device.DeviceID)
	assertOnlyIDs(t, "content_state", decoded["content_state"], device.DeviceID)
	attributes := decoded["attributes"].(map[string]any)
	if attributes["kind"] != LiveKindScan || attributes["pushServerID"] != device.DeviceID || attributes["startedAt"] != float64(f.now.Add(-time.Minute).Unix()) {
		t.Fatalf("attributes = %v", attributes)
	}
	if decoded["attributes_type"] != "AdminJobAttributes" || decoded["event"] != LiveEventStart || decoded["priority"] != "high" {
		t.Fatalf("start = %v", decoded)
	}
}

func (f *adminPushFixture) deviceRowID(serverDeviceID string) string {
	f.t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM bloem_admin_push_devices WHERE server_device_id = $1`, serverDeviceID).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestLiveRequestRefusesANameBeforeTheRelay(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))

	names := []string{"Music videos", "Movies", "edge-b.example.net", "someone@example.test", "lib 7", "7.1", "https://example.test/7", ""}
	for _, name := range names {
		state := runningScan(name, f.now)
		if name == "" {
			state.LibraryID, state.Phase = "", "Scanning Movies"
		}
		if err := f.svc.queueLive(ctx, liveUpdate{Kind: LiveKindScan, SubjectID: "7", Event: LiveEventUpdate, State: state}); err == nil {
			t.Fatalf("a state with %q was queued", name)
		}
	}
	for _, bad := range []adminJobState{
		{Phase: "done", UpdatedAt: f.now.Unix()},
		{Phase: LivePhaseRunning, UpdatedAt: f.now.UnixMilli()},
		{Phase: LivePhaseRunning, UpdatedAt: 0},
		{Phase: LivePhaseRunning, Progress: ptr(1.5), UpdatedAt: f.now.Unix()},
		{Phase: LivePhaseRunning, Processed: ptr(-1), UpdatedAt: f.now.Unix()},
	} {
		if err := bad.validate(); err == nil {
			t.Fatalf("state %+v passed validation", bad)
		}
	}
	if _, _, err := adminLiveStartPayload(&AdminPushDevice{ServerDeviceID: "dev", APNsTopic: AdminPushAppleTopic, APNsEnvironment: APNsEnvironmentSandbox},
		liveStart, "Music videos", runningScan("7", f.now), f.now, f.now.Unix()); err == nil {
		t.Fatal("a start with a name for its kind was built")
	}
	if sent := f.svc.FlushLive(ctx); sent != 0 || len(relay.received()) != 0 {
		t.Fatalf("a refused state reached the relay: flushed %d, calls %d", sent, len(relay.received()))
	}
}

func ptr[T any](v T) *T { return &v }

func TestLiveRequestUsesTheBaseTopicAndMode(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	activity := iosActivity(activityOne, LiveKindScan, "7", liveTokenA)
	activity.Environment = APNsEnvironmentProd
	f.registerLive(admin, device.DeviceID, activity)

	f.queue(LiveKindScan, "7", LiveEventUpdate, runningScan("7", f.now))
	f.svc.FlushLive(ctx)
	calls := relay.received()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	call := calls[0]
	ts := f.now.Unix()
	if call.Path != relayAppleSendPath {
		t.Fatalf("path = %s", call.Path)
	}
	want := map[string]any{
		"token": liveTokenA, "environment": APNsEnvironmentProd, "topic": AdminPushAppleTopic, "mode": "live_activity",
		"server_device_id": device.DeviceID, "delivery_id": device.DeviceID + ".live-" + activityOne + "-" + strconv.FormatInt(ts, 10),
		"event": LiveEventUpdate, "timestamp": float64(ts), "stale_date": float64(ts + 120),
	}
	for key, value := range want {
		if call.Body[key] != value {
			t.Fatalf("%s = %#v, want %#v", key, call.Body[key], value)
		}
	}
	for _, refused := range []string{"badge", "urgency", "attributes", "attributes_type", "dismissal_date", "collapse_id"} {
		if _, ok := call.Body[refused]; ok {
			t.Fatalf("an update carries %s", refused)
		}
	}
	if call.IdempotencyKey == "" {
		t.Fatal("a live send needs an idempotency key")
	}
	if ts < 1 || ts > 1<<32-1 {
		t.Fatalf("timestamp %d is not whole Unix seconds", ts)
	}

	// The end carries the final state and a dismissal 15 minutes on, at high priority.
	f.now = f.now.Add(time.Minute)
	f.queue(LiveKindScan, "7", LiveEventEnd, adminJobState{Phase: LivePhaseFinished, LibraryID: "7", UpdatedAt: f.now.Unix()})
	f.svc.FlushLive(ctx)
	calls = relay.received()
	end := calls[len(calls)-1].Body
	if end["event"] != LiveEventEnd || end["priority"] != "high" || end["dismissal_date"] != float64(f.now.Add(15*time.Minute).Unix()) {
		t.Fatalf("end = %v", end)
	}
	if _, ok := end["stale_date"]; ok {
		t.Fatal("an end carries no stale date")
	}
}

func TestGoneActivityTokenDeletesOnlyThatActivity(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	f.registerLive(admin, device.DeviceID, iosActivity(activityTwo, LiveKindScan, "8", liveTokenB))
	f.registerLive(admin, device.DeviceID, iosActivity(activityThree, LiveKindScan, "9", strings.Repeat("d4", 80)))

	gone := map[string]string{
		liveTokenA: `{"error":{"code":"apns_rejected","message":"APNs rejected the notification: Unregistered","provider_reason":"Unregistered","token_state":"dead"}}`,
		liveTokenB: `{"error":{"code":"apns_rejected","message":"APNs rejected the notification: BadDeviceToken","provider_reason":"BadDeviceToken","token_state":"suspect"}}`,
	}
	relay.mu.Lock()
	relay.answer = func(call relayCall) (int, string, string) {
		if body, ok := gone[call.Body["token"].(string)]; ok {
			return http.StatusUnprocessableEntity, body, ""
		}
		return http.StatusOK, relayAccepted, ""
	}
	relay.mu.Unlock()
	for _, library := range []string{"7", "8", "9"} {
		f.queue(LiveKindScan, library, LiveEventUpdate, runningScan(library, f.now))
	}
	f.svc.FlushLive(ctx)
	if f.liveCount(activityOne) != 0 || f.liveCount(activityTwo) != 0 {
		t.Fatal("an activity whose token is gone must be deleted")
	}
	if f.liveCount(activityThree) != 1 {
		t.Fatal("another activity of the same device was deleted")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1 AND enabled`, device.DeviceID); got != 1 {
		t.Fatal("a gone activity token switched the device off")
	}
	// The device keeps getting alerts.
	if err := f.svc.Notify(ctx, BackupFailedNotice("run-1", "Home", "disk full")); err != nil {
		t.Fatal(err)
	}
	before := len(relay.received())
	f.svc.Drain(ctx)
	if after := relay.received(); len(after) != before+1 || after[len(after)-1].Body["mode"] != relayModePrivateAlert {
		t.Fatalf("the device stopped getting alerts after an activity token went: %d -> %d", before, len(after))
	}
}

func TestGoneStartTokenIsCleared(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	start := liveStart
	if err := f.svc.SetLiveStart(ctx, admin, device.DeviceID, &start, []string{LiveKindScan, LiveKindBackup}); err != nil {
		t.Fatal(err)
	}
	relay.answerAll(http.StatusUnprocessableEntity, `{"error":{"code":"apns_rejected","message":"APNs rejected the notification: ExpiredToken","provider_reason":"ExpiredToken","token_state":"dead"}}`, "")
	loaded, _, err := f.svc.store.deviceByID(ctx, f.deviceRowID(device.DeviceID))
	if err != nil {
		t.Fatal(err)
	}
	if f.svc.sendLiveStart(ctx, loaded, LiveKindScan, runningScan("7", f.now), f.now) {
		t.Fatal("a refused start reported as sent")
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_devices WHERE server_device_id = $1 AND live_start_token_ciphertext IS NULL AND cardinality(live_start_kinds) = 0 AND enabled`, device.DeviceID); got != 1 {
		t.Fatal("a gone start token must be cleared, with its kinds, and leave the device on")
	}
}

func TestRelayWithoutTheModeTurnsSendingOffForAnHour(t *testing.T) {
	for _, code := range []string{"invalid_mode", "unexpected_field"} {
		t.Run(code, func(t *testing.T) {
			f, relay := newLiveRelayFixture(t)
			ctx := context.Background()
			logs := &countingHandler{}
			f.svc.logger = slog.New(logs)
			admin := f.account("root", "admin", true)
			apple := f.register(admin, apnsRegistration("install-root-apple", 'a'))
			android := f.register(admin, fcmRegistration("install-root-droid", 'b'))
			f.registerLive(admin, apple.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
			f.registerLive(admin, android.DeviceID, LiveActivityRegistration{ActivityID: activityTwo, Kind: LiveKindScan, SubjectID: "7", Platform: LivePlatformAndroid})
			relay.mu.Lock()
			relay.answer = func(call relayCall) (int, string, string) {
				if call.Path == relayAppleSendPath {
					return http.StatusBadRequest, `{"error":{"code":"` + code + `","message":"field event is not accepted"}}`, ""
				}
				return http.StatusOK, relayAccepted, ""
			}
			relay.mu.Unlock()

			f.queue(LiveKindScan, "7", LiveEventUpdate, runningScan("7", f.now))
			f.svc.FlushLive(ctx)
			if got := len(relay.received()); got != 2 {
				t.Fatalf("calls = %d, want the Apple and the Android send", got)
			}
			// Within the hour no live request goes to the relay, and Android is unaffected.
			for i := 1; i <= 3; i++ {
				f.now = f.now.Add(15 * time.Minute)
				f.queue(LiveKindScan, "7", LiveEventUpdate, runningScan("7", f.now))
				processed := 120 + i
				state := runningScan("7", f.now)
				state.Processed = &processed
				f.queue(LiveKindScan, "7", LiveEventUpdate, state)
				f.svc.FlushLive(ctx)
			}
			for _, call := range relay.received()[2:] {
				if call.Path == relayAppleSendPath {
					t.Fatal("a live request went to a relay without the mode within the hour")
				}
			}
			if logs.warnings("live_activity") != 1 {
				t.Fatalf("the missing mode was logged %d times, want once", logs.warnings("live_activity"))
			}
			if f.liveCount(activityOne) != 1 {
				t.Fatal("a relay without the mode must not delete the activity")
			}
			// After the hour the server tries again.
			f.now = f.now.Add(20 * time.Minute)
			processed := 400
			state := runningScan("7", f.now)
			state.Processed = &processed
			f.queue(LiveKindScan, "7", LiveEventUpdate, state)
			before := len(relay.received())
			f.svc.FlushLive(ctx)
			appleSends := 0
			for _, call := range relay.received()[before:] {
				if call.Path == relayAppleSendPath {
					appleSends++
				}
			}
			if appleSends != 1 {
				t.Fatalf("after the hour %d live requests went, want 1", appleSends)
			}
		})
	}
}

// countingHandler counts warnings whose message or attributes mention a word.
type countingHandler struct {
	mu      sync.Mutex
	records []string
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countingHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelWarn {
		return nil
	}
	text := r.Message
	r.Attrs(func(a slog.Attr) bool { text += " " + a.Key + "=" + a.Value.String(); return true })
	h.mu.Lock()
	h.records = append(h.records, text)
	h.mu.Unlock()
	return nil
}
func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countingHandler) WithGroup(string) slog.Handler      { return h }
func (h *countingHandler) warnings(word string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if strings.Contains(r, word) {
			n++
		}
	}
	return n
}

func TestLiveRateLimitHoldsSendsForRetryAfter(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	device := f.register(admin, apnsRegistration("install-root-apple", 'a'))
	f.registerLive(admin, device.DeviceID, iosActivity(activityOne, LiveKindScan, "7", liveTokenA))
	relay.answerAll(http.StatusTooManyRequests, `{"error":{"code":"live_activity_rate_limited","message":"too many live sends"}}`, "60")
	f.queue(LiveKindScan, "7", LiveEventUpdate, runningScan("7", f.now))
	f.svc.FlushLive(ctx)
	first := relay.received()
	if len(first) != 1 {
		t.Fatalf("calls = %d", len(first))
	}
	relay.answerAll(http.StatusOK, relayAccepted, "")
	f.now = f.now.Add(45 * time.Second)
	f.svc.FlushLive(ctx)
	if got := len(relay.received()); got != 1 {
		t.Fatalf("a send went %d s into a 60 s Retry-After", 45)
	}
	f.now = f.now.Add(16 * time.Second)
	f.svc.FlushLive(ctx)
	calls := relay.received()
	if len(calls) != 2 {
		t.Fatalf("the held state was not sent after Retry-After: calls = %d", len(calls))
	}
	if calls[1].Body["timestamp"].(float64) <= first[0].Body["timestamp"].(float64) {
		t.Fatal("the retry must carry a larger timestamp")
	}
	if f.liveCount(activityOne) != 1 {
		t.Fatal("a rate limit must not delete the activity")
	}
}

func TestAndroidActivityGetsAPrivateAlertWithALiveDeliveryID(t *testing.T) {
	f, relay := newLiveRelayFixture(t)
	ctx := context.Background()
	admin := f.account("root", "admin", true)
	android := f.register(admin, fcmRegistration("install-root-droid", 'b'))
	f.registerLive(admin, android.DeviceID, LiveActivityRegistration{ActivityID: activityOne, Kind: LiveKindBackup, Platform: LivePlatformAndroid})
	f.queue(LiveKindBackup, "", LiveEventUpdate, adminJobState{Phase: LivePhaseRunning, Progress: ptr(0.25), UpdatedAt: f.now.Unix()})
	f.svc.FlushLive(ctx)
	calls := relay.received()
	if len(calls) != 1 || calls[0].Path != relayFcmSendPath {
		t.Fatalf("calls = %+v", calls)
	}
	body := calls[0].Body
	want := android.DeviceID + ".live-" + activityOne + "-" + strconv.FormatInt(f.now.Unix(), 10)
	if body["mode"] != relayModePrivateAlert || body["delivery_id"] != want || body["token"] != strings.Repeat("b", 64) || body["server_device_id"] != android.DeviceID {
		t.Fatalf("android live send = %v, want delivery id %s", body, want)
	}
	for key := range body {
		switch key {
		case "token", "mode", "server_device_id", "delivery_id":
		default:
			t.Fatalf("an Android live send carries %s; only an id may cross the relay", key)
		}
	}
	if _, ok := body["urgency"]; ok {
		t.Fatal("an Android live send is high priority: no urgency")
	}
}
