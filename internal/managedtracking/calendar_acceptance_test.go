package managedtracking

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	cal "github.com/Silo-Server/silo-server/internal/managedtracking/calendarwire"
)

type realCalendarFixtureSource struct{}

func (realCalendarFixtureSource) Authority(_ context.Context, p Profile) (string, error) {
	return "scope:" + p.AccountID + ":" + p.ProfileID, nil
}
func (realCalendarFixtureSource) Events(_ context.Context, p Profile, start, end time.Time) ([]*cal.CalendarEvent, error) {
	day := time.Now().UTC().Format("2006-01-02")
	if day < start.Format("2006-01-02") || day > end.Format("2006-01-02") {
		return nil, nil
	}
	out := make([]*cal.CalendarEvent, 0, 101)
	for i := 0; i < 101; i++ {
		//nolint:misspell // The generated wire field retains the approved protocol spelling.
		out = append(out, &cal.CalendarEvent{EventId: fmt.Sprintf("%s-event-%d", p.ProfileID, i), ContentId: fmt.Sprintf("item-%d", i), Kind: "movie", Title: fmt.Sprintf("Movie %d", i), Date: day, Followed: i%2 == 0, Favourite: i%3 == 0, Watchlist: i%4 == 0})
	}
	return out, nil
}
func TestRealPluginCalendarPublishesAllWindowsAndPagedEvents(t *testing.T) {
	pluginBinary, fixtureBinary := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if pluginBinary == "" || fixtureBinary == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.SetCalendarSource(realCalendarFixtureSource{})
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, pluginBinary, fixtureBinary)
	run.start()
	run.reconcile()
	consumer, err := run.client.EventConsumer("pastime.lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = consumer.HandleEvent(ctx, &pluginv1.HandleEventRequest{EventName: "profile.upsert"}); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var out map[string]struct{ Windows, Events int }
		status, _ := run.call("GET", "/fixture/calendar", nil, &out)
		if status != 200 {
			t.Fatalf("fixture calendar: %d", status)
		}
		got := out[p.ProfileID]
		if got.Windows == 7 && got.Events == 101 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("calendar did not converge: %+v", out)
		case <-ticker.C:
		}
	}
	if err = run.host.Stop(id); err != nil {
		t.Fatal(err)
	}
	run.start()
	run.reconcile()
	var after map[string]struct{ Windows, Events int }
	run.call("GET", "/fixture/calendar", nil, &after)
	if after[p.ProfileID].Events != 101 {
		t.Fatalf("restart duplicated events: %+v", after)
	}
	run.finish()
}
