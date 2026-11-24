package notifications

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AdminPushQuietHoursCapabilityToken tells an app this server keeps a
// device's quiet hours. It is advertised with AdminPushCapabilityToken.
const AdminPushQuietHoursCapabilityToken = "admin_push_quiet_hours_v1"

// AdminPushQuietHours is a device's quiet window: from Start to End
// ("HH:MM", 24-hour, may cross midnight) in TimeZone (IANA), the server
// sends the device only the kinds in AllowKinds, and drops the others
// instead of holding them. The two reachability kinds are the apps' own;
// the apps apply quiet hours to them.
type AdminPushQuietHours struct {
	Start      string   `json:"start"`
	End        string   `json:"end"`
	TimeZone   string   `json:"time_zone"`
	AllowKinds []string `json:"allow_kinds"`
}

// quietWindow is a parsed quiet window.
type quietWindow struct {
	startMinute, endMinute int
	timeZone               string
	location               *time.Location
	allowKinds             []string
}

func parseQuietClock(value string) (int, bool) {
	hour, minute, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || len(hour) != 2 || len(minute) != 2 {
		return 0, false
	}
	h, err1 := strconv.Atoi(hour)
	m, err2 := strconv.Atoi(minute)
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func quietClock(minute int) string { return fmt.Sprintf("%02d:%02d", minute/60, minute%60) }

// parseQuietHours validates q.
func parseQuietHours(q AdminPushQuietHours) (quietWindow, error) {
	start, ok := parseQuietClock(q.Start)
	if !ok {
		return quietWindow{}, fmt.Errorf("%w: start must be HH:MM", ErrAdminPushInvalid)
	}
	end, ok := parseQuietClock(q.End)
	if !ok {
		return quietWindow{}, fmt.Errorf("%w: end must be HH:MM", ErrAdminPushInvalid)
	}
	if start == end {
		return quietWindow{}, fmt.Errorf("%w: start and end must differ", ErrAdminPushInvalid)
	}
	zone := strings.TrimSpace(q.TimeZone)
	if zone == "" || zone == "Local" {
		return quietWindow{}, fmt.Errorf("%w: time_zone must be an IANA time zone", ErrAdminPushInvalid)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return quietWindow{}, fmt.Errorf("%w: unknown time_zone %q", ErrAdminPushInvalid, zone)
	}
	allow, err := validKindIDs(q.AllowKinds)
	if err != nil {
		return quietWindow{}, err
	}
	return quietWindow{startMinute: start, endMinute: end, timeZone: zone, location: location, allowKinds: allow}, nil
}

func (w quietWindow) hours() AdminPushQuietHours {
	return AdminPushQuietHours{Start: quietClock(w.startMinute), End: quietClock(w.endMinute), TimeZone: w.timeZone, AllowKinds: nonNil(w.allowKinds)}
}

// inQuietHours reports whether now falls in the window from startMin to
// endMin (minutes of the day in tz). A window whose start is after its end
// crosses midnight; an empty window holds nothing.
func inQuietHours(now time.Time, startMin, endMin int, tz *time.Location) bool {
	if startMin == endMin || tz == nil {
		return false
	}
	local := now.In(tz)
	minute := local.Hour()*60 + local.Minute()
	if startMin < endMin {
		return minute >= startMin && minute < endMin
	}
	return minute >= startMin || minute < endMin
}

// holds reports whether the window keeps kind from the device at now.
func (w quietWindow) holds(now time.Time, kind string) bool {
	if !inQuietHours(now, w.startMinute, w.endMinute, w.location) {
		return false
	}
	for _, allowed := range w.allowKinds {
		if allowed == kind {
			return false
		}
	}
	return true
}

// SetQuietHours stores one of the caller's devices' quiet window.
func (s *AdminPushService) SetQuietHours(ctx context.Context, userID int, deviceID string, q AdminPushQuietHours) (AdminPushQuietHours, error) {
	if !s.Available() {
		return AdminPushQuietHours{}, ErrAdminPushUnavailable
	}
	window, err := parseQuietHours(q)
	if err != nil {
		return AdminPushQuietHours{}, err
	}
	found, err := s.store.setQuietHours(ctx, userID, deviceID, &window)
	if err != nil {
		return AdminPushQuietHours{}, err
	}
	if !found {
		return AdminPushQuietHours{}, ErrAdminPushNotFound
	}
	return window.hours(), nil
}

// ClearQuietHours removes one of the caller's devices' quiet window.
func (s *AdminPushService) ClearQuietHours(ctx context.Context, userID int, deviceID string) error {
	if !s.Available() {
		return ErrAdminPushUnavailable
	}
	found, err := s.store.setQuietHours(ctx, userID, deviceID, nil)
	if err != nil {
		return err
	}
	if !found {
		return ErrAdminPushNotFound
	}
	return nil
}
