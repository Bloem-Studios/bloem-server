package livetv

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func xtreamGuideFixture() (time.Time, []Channel, string) {
	start := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	channels := []Channel{{ID: "channel-a", Number: "2147483648", GuideStationID: "station-a", Enabled: true}, {ID: "channel-b", Number: "22", Enabled: true}, {ID: "disabled", GuideStationID: "station-a"}}
	entry := `<programme channel="station-a" start="20260919140000 +0200" stop="20260919150000 +0200"><title lang="nl">Nieuws</title><title lang="en">News &amp; Weather</title><sub-title>Evening</sub-title><desc>Forecast</desc><category>News</category><episode-num system="xmltv_ns">0.2/12.</episode-num><new/><live/><icon src="http://metadata/secret"/></programme>`
	return start, channels, entry
}

func TestBloemXtreamXMLTVIdentityMappingAndMetadata(t *testing.T) {
	start, channels, entry := xtreamGuideFixture()
	var fetched atomic.Int32
	dtd := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { fetched.Add(1) }))
	defer dtd.Close()
	feed := "\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"UTF-8\"?><!DOCTYPE tv SYSTEM \"" + dtd.URL + "/xmltv.dtd\"><tv><channel id=\"station-a\"><display-name>Station</display-name></channel>" + entry + entry +
		`<programme channel="22" start="20260919120000" stop="20260919123000"><title>Fallback ID</title><premiere/><previously-shown/></programme>` +
		`<programme channel="foreign" start="invalid"><title>Not our station</title></programme></tv>`
	programs, _, err := parseXtreamGuide(strings.NewReader(feed), "source-a", channels, start.Add(-time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(programs) != 2 {
		t.Fatalf("programmes = %d, want 2", len(programs))
	}
	first := programs[0]
	if first.Title != "News & Weather" || first.ChannelID != "channel-a" || !first.Start.Equal(start) || !first.Stop.Equal(start.Add(time.Hour)) || first.Subtitle != "Evening" || first.Description != "Forecast" || !first.IsNew || !first.IsLive || first.Season == nil || *first.Season != 1 || first.Episode == nil || *first.Episode != 3 || first.ImageURL != "" {
		t.Fatalf("unexpected programme: %+v", first)
	}
	if programs[1].ChannelID != "channel-b" || programs[1].IsNew {
		t.Fatal("fallback mapping or previously-shown semantics changed")
	}
	if fetched.Load() != 0 {
		t.Fatal("XMLTV resolved an external DTD")
	}
	updated, _, err := parseXtreamGuide(strings.NewReader("<tv>"+strings.ReplaceAll(entry, "News &amp; Weather", "Corrected title")+"</tv>"), "source-a", channels, start, start.Add(time.Hour))
	if err != nil || len(updated) != 1 || updated[0].ID != first.ID {
		t.Fatalf("correction did not preserve programme identity: %v", err)
	}
	other, _, err := parseXtreamGuide(strings.NewReader("<tv>"+entry+"</tv>"), "source-b", channels, start, start.Add(time.Hour))
	if err != nil || len(other) != 1 || other[0].ID == first.ID {
		t.Fatalf("programme identity crossed sources: %v", err)
	}
}

func TestBloemXtreamXMLTVRejectsInvalidFeeds(t *testing.T) {
	start, channels, entry := xtreamGuideFixture()
	cases := map[string]string{
		"html":               "<html>private-marker</html>",
		"incomplete":         "<tv>" + entry,
		"extra root":         "<tv>" + entry + "</tv><tv/>",
		"outside text":       "private-marker<tv/>",
		"internal entity":    `<!DOCTYPE tv [<!ENTITY private SYSTEM "file:///private-marker">]><tv/>`,
		"nested declaration": `<tv><channel><!DOCTYPE tv [<!ENTITY private "private-marker">]></channel></tv>`,
		"unknown entity":     "<tv>" + strings.ReplaceAll(entry, "Forecast", "&private-marker;") + "</tv>",
	}
	for name, feed := range cases {
		t.Run(name, func(t *testing.T) {
			programs, _, err := parseXtreamGuide(strings.NewReader(feed), "source", channels, start.Add(-time.Hour), start.Add(time.Hour))
			if err == nil || len(programs) != 0 {
				t.Fatal("invalid feed was accepted or partially returned")
			}
			if strings.Contains(err.Error(), "private-marker") {
				t.Fatal("raw provider data was exposed by the parser error")
			}
		})
	}
}

// A messy provider feed must still publish its valid programmes. Entry-level
// defects are skipped and counted by reason; only structural problems fail.
func TestBloemXtreamXMLTVSkipsInvalidProgrammes(t *testing.T) {
	start, channels, entry := xtreamGuideFixture()
	at := func(from, to string) string {
		// entry runs 14:00-15:00 +0200 (12:00-13:00 UTC); shift it to a new slot.
		e := strings.ReplaceAll(entry, `start="20260919140000 +0200"`, `start="`+from+`"`)
		return strings.ReplaceAll(e, `stop="20260919150000 +0200"`, `stop="`+to+`"`)
	}
	inWindow := []string{
		entry, // valid
		strings.ReplaceAll(entry, "News &amp; Weather", "Other programme"), // conflicting identity
		at("20260919110000", "private-marker"),                             // invalid timestamp
		at("20260919110500", "20260919110500"),                             // zero length
		at("20260919111000", "20260919103000"),                             // stop before start
		at("20260919112000", "20260922112000"),                             // > 48h
		strings.ReplaceAll(at("20260919113000", "20260919114000"), "News &amp; Weather", strings.Repeat("x", 1025)), // long title
		strings.ReplaceAll(at("20260919114000", "20260919115000"), "<category>News", "<category>"+strings.Repeat("x", 257)),
		strings.ReplaceAll(at("20260919115000", "20260919115500"), "<title lang=\"nl\">Nieuws</title><title lang=\"en\">News &amp; Weather</title>", ""), // no title
	}
	outOfWindow := []string{
		at("20260918000000", "20260918000000"), // zero length, long before the window
		at("20260925000000", "20260929000000"), // > 48h, long after the window
		strings.ReplaceAll(at("20260925000000", "20260925010000"), "News &amp; Weather", strings.Repeat("x", 1025)), // oversized, after
	}
	feed := "<tv>" + strings.Join(inWindow, "") + strings.Join(outOfWindow, "") + "</tv>"
	programs, skipped, err := parseXtreamGuide(strings.NewReader(feed), "source", channels, start.Add(-2*time.Hour), start.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("messy feed failed the whole sync: %v", err)
	}
	if len(programs) != 1 || programs[0].Title != "News & Weather" {
		t.Fatalf("programmes = %+v, want only the valid entry", programs)
	}
	want := xtreamGuideSkips{InvalidTimestamp: 1, InvalidDuration: 3, MissingTitle: 1, OversizedMetadata: 2, ConflictingIdentity: 1}
	if skipped != want {
		t.Fatalf("skips = %+v, want %+v", skipped, want)
	}
	if skipped.Total() != 8 {
		t.Fatalf("total = %d", skipped.Total())
	}
	if summary := skipped.String(); strings.Contains(summary, "private-marker") || !strings.Contains(summary, "3 invalid duration") {
		t.Fatalf("summary leaks provider data or omits counts: %q", summary)
	}
}

func TestBloemXtreamXMLTVWindowAndEpisodeBounds(t *testing.T) {
	start, channels, entry := xtreamGuideFixture()
	programs, _, err := parseXtreamGuide(strings.NewReader("<tv>"+entry+"</tv>"), "source", channels, start.Add(time.Hour), start.Add(2*time.Hour))
	if err != nil || len(programs) != 0 {
		t.Fatalf("non-overlapping programme retained: %v", err)
	}
	for _, value := range []string{"", "-1", "100000", "not-a-number"} {
		if xtreamEpisodeIndex(value) != nil {
			t.Fatalf("invalid episode %q accepted", value)
		}
	}
	for _, stamp := range []string{"20260919120000", "20260919120000 +0000", "20260919140000+0200"} {
		got, err := xtreamXMLTime(stamp)
		if err != nil || !got.Equal(start) {
			t.Fatalf("timestamp %q = %s, %v", stamp, got, err)
		}
	}
}

type xtreamWhitespace struct{}

func (xtreamWhitespace) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestBloemXtreamXMLTVSizeBound(t *testing.T) {
	start, channels, entry := xtreamGuideFixture()
	feed := io.MultiReader(strings.NewReader("<tv>"+entry), io.LimitReader(xtreamWhitespace{}, xtreamXMLLimit), strings.NewReader("</tv>"))
	if _, _, err := parseXtreamGuide(feed, "source", channels, start, start.Add(time.Hour)); err == nil {
		t.Fatal("oversized XMLTV feed accepted")
	}
}

func TestBloemXtreamBlockedDestinations(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "[::]", "[::ffff:0.0.0.0]", "[fd00:ec2::254]"} {
		if err := ValidateMediaFetchURL(fmt.Sprintf("http://%s", host)); err == nil {
			t.Fatalf("unsafe destination accepted: %s", host)
		}
	}
	for _, host := range []string{"192.168.1.23", "169.254.1.23"} {
		if err := ValidateMediaFetchURL("http://" + host); err != nil {
			t.Fatalf("legitimate LAN tuner rejected: %v", err)
		}
	}
}
