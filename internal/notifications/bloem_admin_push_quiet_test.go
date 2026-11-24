package notifications

import (
	"errors"
	"testing"
	"time"
)

func TestInQuietHours(t *testing.T) {
	amsterdam, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	start, end := 22*60, 7*60
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 11, day, hour, minute, 0, 0, amsterdam) }
	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"23:30", at(24, 23, 30), true},
		{"06:59", at(25, 6, 59), true},
		{"07:00", at(25, 7, 0), false},
		{"21:59", at(24, 21, 59), false},
		{"22:00", at(24, 22, 0), true},
		{"midday", at(24, 12, 0), false},
	} {
		if got := inQuietHours(tc.now, start, end, amsterdam); got != tc.want {
			t.Errorf("%s: inQuietHours = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The window follows the zone across the change from summer time on
	// 2026-10-25: 05:30 UTC is 07:30 on the 24th (CEST) and 06:30 on the 25th (CET).
	if inQuietHours(time.Date(2026, 10, 24, 5, 30, 0, 0, time.UTC), start, end, amsterdam) {
		t.Error("07:30 CEST is inside 22:00-07:00")
	}
	if !inQuietHours(time.Date(2026, 10, 25, 5, 30, 0, 0, time.UTC), start, end, amsterdam) {
		t.Error("06:30 CET is outside 22:00-07:00")
	}
	// A window within one day.
	if !inQuietHours(at(24, 13, 0), 12*60, 14*60, amsterdam) || inQuietHours(at(24, 14, 0), 12*60, 14*60, amsterdam) {
		t.Error("a 12:00-14:00 window is wrong")
	}
	if inQuietHours(at(24, 13, 0), 9*60, 9*60, amsterdam) {
		t.Error("an empty window holds pushes")
	}
}

func TestParseQuietHours(t *testing.T) {
	q, err := parseQuietHours(AdminPushQuietHours{Start: "22:00", End: "07:00", TimeZone: "Europe/Amsterdam", AllowKinds: []string{AdminKindNodeOffline, AdminKindNodeOffline}})
	if err != nil {
		t.Fatal(err)
	}
	if q.startMinute != 1320 || q.endMinute != 420 || q.timeZone != "Europe/Amsterdam" || len(q.allowKinds) != 1 {
		t.Fatalf("quiet hours = %+v", q)
	}
	for name, in := range map[string]AdminPushQuietHours{
		"hour 24":        {Start: "24:00", End: "07:00", TimeZone: "UTC"},
		"no colon":       {Start: "2200", End: "07:00", TimeZone: "UTC"},
		"same times":     {Start: "07:00", End: "07:00", TimeZone: "UTC"},
		"unknown zone":   {Start: "22:00", End: "07:00", TimeZone: "Mars/Olympus"},
		"no zone":        {Start: "22:00", End: "07:00"},
		"local zone":     {Start: "22:00", End: "07:00", TimeZone: "Local"},
		"unknown kind":   {Start: "22:00", End: "07:00", TimeZone: "UTC", AllowKinds: []string{"admin.nope"}},
		"minute too big": {Start: "22:60", End: "07:00", TimeZone: "UTC"},
	} {
		if _, err := parseQuietHours(in); !errors.Is(err, ErrAdminPushInvalid) {
			t.Errorf("%s: err = %v, want ErrAdminPushInvalid", name, err)
		}
	}
}
