package notifications

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Organization admins may register for the two limit kinds and receive
// them for their own organization's members only.
func TestLimitAlertsReachTheMembersOrganizationAdmins(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	platform := f.account("limits-platform", "admin", true)
	orgAdminA := f.account("limits-org-admin-a", "user", true)
	orgAdminB := f.account("limits-org-admin-b", "user", true)
	member := f.account("limits-member-a", "user", true)
	orgA := f.org(orgAdminA, "limits-org-a")
	orgB := f.org(orgAdminB, "limits-org-b")
	f.member(orgA, orgAdminA, "admin")
	f.member(orgA, member, "user")
	f.member(orgB, orgAdminB, "admin")

	f.register(platform, fcmRegistration("install-limits-platform", 'p'))
	a := f.register(orgAdminA, fcmRegistration("install-limits-org-a", 'a'))
	f.register(orgAdminB, fcmRegistration("install-limits-org-b", 'b'))
	if _, err := f.svc.Register(ctx, member, fcmRegistration("install-limits-member", 'm')); !errors.Is(err, ErrAdminPushForbidden) {
		t.Fatalf("a member who is no admin registered: %v", err)
	}
	if len(a.Kinds) != 2 {
		t.Fatalf("organization admin kinds = %+v, want only the two limit kinds", a.Kinds)
	}
	for _, k := range a.Kinds {
		if kind, _ := AdminPushKindByID(k.Kind); !kind.OrganizationAdmins {
			t.Fatalf("organization admin offered %s", k.Kind)
		}
	}
	if _, err := f.svc.SetKinds(ctx, orgAdminA, a.DeviceID, map[string]bool{AdminKindTranscodeLimitReached: true}); err != nil {
		t.Fatalf("organization admin switching on a limit kind: %v", err)
	}
	if _, err := f.svc.SetKinds(ctx, orgAdminA, a.DeviceID, map[string]bool{AdminKindScanFailed: true}); !errors.Is(err, ErrAdminPushKindNotAvailable) {
		t.Fatalf("organization admin switching on scan failures: %v, want ErrAdminPushKindNotAvailable", err)
	}

	notice := TranscodeLimitNotice(member, "limits-member-a", 2, "person", f.now)
	notice.OrganizationID = uuid.MustParse(orgA)
	if err := f.svc.Notify(ctx, notice); err != nil {
		t.Fatal(err)
	}
	for who, want := range map[int]int{platform: 1, orgAdminA: 1, orgAdminB: 0} {
		if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE user_id = $1 AND kind = $2`, who, AdminKindTranscodeLimitReached); got != want {
			t.Fatalf("account %d got %d limit deliveries, want %d", who, got, want)
		}
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_attempts a JOIN bloem_admin_push_devices d ON d.id = a.device_id WHERE d.user_id = $1`, orgAdminA); got != 1 {
		t.Fatalf("organization admin attempts = %d, want 1", got)
	}
	if err := f.svc.Notify(ctx, BackupFailedNotice("limits-run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE user_id = $1 AND kind = $2`, orgAdminA, AdminKindBackupFailed); got != 0 {
		t.Fatalf("organization admin got %d backup deliveries, want none", got)
	}
	nobody := StreamLimitNotice(member, "limits-member-a", 1, "person", f.now)
	if err := f.svc.Notify(ctx, nobody); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindStreamLimitReached); got != 1 {
		t.Fatalf("a notice with no organization reached %d accounts, want the platform admin only", got)
	}
}

// A device in its quiet hours gets only the kinds it lets through.
func TestQuietHoursHoldKindsOutsideTheAllowList(t *testing.T) {
	f := newAdminPushFixture(t)
	ctx := context.Background()
	admin := f.account("quiet-admin", "admin", true)
	phone := f.register(admin, fcmRegistration("install-quiet-admin", 'q'))
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 11, 24, 23, 30, 0, 0, amsterdam)
	set, err := f.svc.SetQuietHours(ctx, admin, phone.DeviceID, AdminPushQuietHours{
		Start: "22:00", End: "07:00", TimeZone: "Europe/Amsterdam", AllowKinds: []string{AdminKindNodeOffline}})
	if err != nil {
		t.Fatal(err)
	}
	if set.Start != "22:00" || set.End != "07:00" || set.TimeZone != "Europe/Amsterdam" || len(set.AllowKinds) != 1 {
		t.Fatalf("quiet hours = %+v", set)
	}
	if err := f.svc.Notify(ctx, BackupFailedNotice("quiet-run-1", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed); got != 0 {
		t.Fatalf("a held kind was queued %d times in the quiet hours", got)
	}
	if err := f.svc.Notify(ctx, NodeOfflineNotice("4", "a310", f.now)); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindNodeOffline); got != 1 {
		t.Fatalf("an allowed kind was queued %d times, want 1", got)
	}
	if _, err := f.svc.SetQuietHours(ctx, admin+1000, phone.DeviceID, AdminPushQuietHours{Start: "22:00", End: "07:00", TimeZone: "UTC"}); !errors.Is(err, ErrAdminPushNotFound) {
		t.Fatalf("another account set this device's quiet hours: %v", err)
	}
	if err := f.svc.ClearQuietHours(ctx, admin, phone.DeviceID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Notify(ctx, BackupFailedNotice("quiet-run-2", "Home", "")); err != nil {
		t.Fatal(err)
	}
	if got := f.count(`SELECT count(*) FROM bloem_admin_push_deliveries WHERE kind = $1`, AdminKindBackupFailed); got != 1 {
		t.Fatalf("after clearing the quiet hours the kind was queued %d times, want 1", got)
	}
}
