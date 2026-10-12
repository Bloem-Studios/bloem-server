package managedtracking

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestRealPluginAvailabilityReturnsAuthorizedHandoff(t *testing.T) {
	binary, fixture := os.Getenv("BLOEM_MANAGED_PLUGIN_BINARY"), os.Getenv("BLOEM_MANAGED_PASTIME_FIXTURE_BINARY")
	if binary == "" || fixture == "" {
		t.Skip("explicit independently built plugin and Pastime fixture required")
	}
	s, id, p := enrollmentFixture(t)
	s.Calendar = &availabilityFixtureSource{calendarFixtureSource: calendarFixtureSource{digest: "scope"}}
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Second)
	defer cancel()
	run := startPastimeAcceptance(t, ctx, s, id, binary, fixture)
	run.start()
	run.reconcile()
	read := func() string {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, "GET", run.url+"/fixture/availability?profile="+url.QueryEscape(p.ProfileID), nil)
		if e != nil {
			t.Fatal(e)
		}
		res, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			body, _ := io.ReadAll(res.Body)
			t.Fatal("fixture read", res.StatusCode, string(body))
		}
		var out struct {
			Items []struct {
				Status  string `json:"status"`
				Handoff *struct {
					URL string `json:"url"`
				} `json:"handoff"`
			} `json:"items"`
		}
		if e = json.NewDecoder(res.Body).Decode(&out); e != nil || len(out.Items) != 1 {
			t.Fatal("fixture data", e)
		}
		if out.Items[0].Status == "available" && (out.Items[0].Handoff == nil || out.Items[0].Handoff.URL != "https://lib.strm.cafe/item/synthetic-item") {
			t.Fatal("incorrect admin handoff")
		}
		return out.Items[0].Status
	}
	if got := read(); got != "checking" {
		t.Fatal("first lookup", got)
	}
	run.reconcile()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for read() != "available" {
		select {
		case <-ctx.Done():
			t.Fatal("availability did not publish")
		case <-ticker.C:
		}
	}
}
