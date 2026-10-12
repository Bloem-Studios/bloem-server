package managedtracking

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	calendar "github.com/Silo-Server/silo-server/internal/managedtracking/calendarwire"
)

type calendarFixtureSource struct {
	digest    string
	events    []*calendar.CalendarEvent
	afterRead func()
}

func (f *calendarFixtureSource) Authority(context.Context, Profile) (string, error) {
	if f.digest == "" {
		return "", ErrAuthority
	}
	return f.digest, nil
}
func (f *calendarFixtureSource) Events(context.Context, Profile, time.Time, time.Time) ([]*calendar.CalendarEvent, error) {
	if f.afterRead != nil {
		f.afterRead()
	}
	return f.events, nil
}

func TestCalendarPagesRequireCompleteTraversalAndStableScope(t *testing.T) {
	f := enrolledFixture(t)
	source := &calendarFixtureSource{digest: "visible-scope", events: []*calendar.CalendarEvent{{EventId: "first", ContentId: "first", Date: "2026-10-12"}, {EventId: "second", ContentId: "second", Date: "2026-10-13"}, {EventId: "third", ContentId: "third", Date: "2026-10-14"}}}
	f.s.Calendar = source
	r := &calendar.CalendarRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "2026-10-12", End: "2026-10-14", Limit: 1}
	page, err := f.s.ReadCalendar(t.Context(), f.id, r)
	if err != nil {
		t.Fatal(err)
	}
	if page.Complete || len(page.Events) != 1 || page.Events[0].ContentId != "first" || page.Revision == 0 {
		t.Fatalf("invalid first page: %v", page)
	}
	r.SnapshotId = page.SnapshotId
	r.Cursor = "2"
	if _, err = f.s.ReadCalendar(t.Context(), f.id, r); !errors.Is(err, ErrSnapshot) {
		t.Fatalf("skipped traversal accepted: %v", err)
	}
	r.Cursor = page.NextCursor
	second, err := f.s.ReadCalendar(t.Context(), f.id, r)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.ReadCalendar(t.Context(), f.id, r)
	if err != nil || replay.NextCursor != second.NextCursor || replay.Events[0].ContentId != "second" {
		t.Fatalf("page replay changed: %v %v", replay, err)
	}
	source.digest = "different-scope"
	r.Cursor = second.NextCursor
	if _, err = f.s.ReadCalendar(t.Context(), f.id, r); !errors.Is(err, ErrAuthority) {
		t.Fatalf("changed authority still exports: %v", err)
	}
}
func TestCalendarRechecksAuthorityAfterGeneration(t *testing.T) {
	f := enrolledFixture(t)
	source := &calendarFixtureSource{digest: "old", events: []*calendar.CalendarEvent{{EventId: "private", ContentId: "private", Date: "2026-10-12"}}}
	source.afterRead = func() { source.digest = "new" }
	f.s.Calendar = source
	_, err := f.s.ReadCalendar(t.Context(), f.id, &calendar.CalendarRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "2026-10-12", End: "2026-10-12", Limit: 100})
	if !errors.Is(err, ErrAuthority) {
		t.Fatalf("authority changed during generation: %v", err)
	}
}
func TestCalendarRejectsForeignProfileAndMalformedWindow(t *testing.T) {
	f := enrolledFixture(t)
	f.s.Calendar = &calendarFixtureSource{digest: "scope"}
	for _, r := range []*calendar.CalendarRequest{
		{AccountId: f.p.AccountID, ProfileId: "foreign", Start: "2026-10-12", End: "2026-10-12", Limit: 1},
		{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "2026-10-12", End: "2026-12-12", Limit: 1},
		{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "bad", End: "2026-10-12", Limit: 1},
	} {
		if _, err := f.s.ReadCalendar(t.Context(), f.id, r); err == nil {
			t.Fatalf("invalid read accepted: %v", r)
		}
	}
}

func TestCalendarLeaseCannotRenewChangedContentVisibility(t *testing.T) {
	f := enrolledFixture(t)
	source := &calendarFixtureSource{digest: "scope", events: []*calendar.CalendarEvent{{EventId: "adult", ContentId: "adult", Date: "2026-10-12"}}}
	f.s.Calendar = source
	page, err := f.s.ReadCalendar(t.Context(), f.id, &calendar.CalendarRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "2026-10-12", End: "2026-10-12", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	request := &calendar.CalendarAuthorityRequest{Profiles: []*calendar.CalendarAuthority{{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, VisibilityDigest: page.VisibilityDigest}}}
	before, err := f.s.CheckCalendarAuthority(t.Context(), f.id, request)
	if err != nil || len(before.Authorized) != 1 {
		t.Fatalf("unchanged content not authorized: %v %v", before, err)
	}
	source.events = nil // Newly restricted item, unchanged profile policy.
	after, err := f.s.CheckCalendarAuthority(t.Context(), f.id, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Authorized) != 0 {
		t.Fatal("lease renewed after catalog item became inaccessible")
	}
}

func TestCalendarWindowUsesTimedInstantWithoutChangingSourceDate(t *testing.T) {
	f := enrolledFixture(t)
	instant, err := time.Parse(time.RFC3339, "2026-03-29T02:30:00Z")
	if err != nil {
		t.Fatal(err)
	}
	f.s.Calendar = &calendarFixtureSource{digest: "scope", events: []*calendar.CalendarEvent{{EventId: "episode", ContentId: "episode", Date: "2026-03-28", AirAt: timestamppb.New(instant), AirTimezone: "America/New_York"}}}
	page, err := f.s.ReadCalendar(t.Context(), f.id, &calendar.CalendarRequest{AccountId: f.p.AccountID, ProfileId: f.p.ProfileID, Start: "2026-03-29", End: "2026-03-29", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Date != "2026-03-28" {
		t.Fatal("timed source date was changed or event omitted")
	}
}

func TestCalendarCapabilityAppearsOnlyAfterSourceComposition(t *testing.T) {
	f := enrolledFixture(t)
	b := &brokerService{service: f.s, installation: f.id}
	before, err := b.GetInfo(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.ExtensionCapabilities) != 0 {
		t.Fatal("unconfigured calendar advertised")
	}
	f.s.SetCalendarSource(&calendarFixtureSource{digest: "scope"})
	after, err := b.GetInfo(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.ExtensionCapabilities) != 1 || after.ExtensionCapabilities[0] != "bloem.calendar.v1" {
		t.Fatal("configured optional service not advertised")
	}
}
