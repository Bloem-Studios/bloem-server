package notifications

import (
	"context"
	"errors"
	"slices"
	"strings"
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
