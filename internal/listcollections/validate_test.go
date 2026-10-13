package listcollections

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var snapshotNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func validPage(t *testing.T) Page {
	t.Helper()
	entries := []Entry{{Kind: "movie", Title: "Synthetic movie", Year: 2020, ExternalIDs: map[string]string{"tmdb": "42"}}}
	digest, err := Digest(entries)
	if err != nil {
		t.Fatal(err)
	}
	return Page{Version: 1, JobID: "job", BindingID: "binding", Generation: 1,
		SourceID: "list:1", SnapshotID: "snapshot", LeaseToken: "lease", Total: 1,
		GeneratedAt: snapshotNow, ExpiresAt: snapshotNow.Add(time.Minute),
		Final: true, Entries: entries, Digest: digest}
}

func TestListSnapshotValidation(t *testing.T) {
	if err := ValidatePageAt(validPage(t), snapshotNow); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*Page)
	}{
		{"partial final", func(p *Page) { p.Total = 2 }},
		{"past end", func(p *Page) { p.Position = 1 }},
		{"negative position", func(p *Page) { p.Position = -1 }},
		{"expired", func(p *Page) { p.ExpiresAt = snapshotNow }},
		{"future generation", func(p *Page) { p.GeneratedAt = snapshotNow.Add(time.Second) }},
		{"long lease", func(p *Page) { p.ExpiresAt = snapshotNow.Add(121 * time.Second) }},
		{"final cursor", func(p *Page) { p.NextCursor = "more" }},
		{"missing cursor", func(p *Page) { p.Final = false; p.Total = 2 }},
		{"nonprogressing empty page", func(p *Page) { p.Final = false; p.NextCursor = "more"; p.Entries = nil }},
		{"unknown version", func(p *Page) { p.Version = 2 }},
		{"zero generation", func(p *Page) { p.Generation = 0 }},
		{"foreign kind identity", func(p *Page) { p.Entries[0].ExternalIDs = map[string]string{"isbn": "9780140328721"} }},
		{"unknown kind", func(p *Page) { p.Entries[0].Kind = "podcast" }},
		{"empty identity", func(p *Page) { p.Entries[0].ExternalIDs = nil }},
		{"noncanonical identity", func(p *Page) { p.Entries[0].ExternalIDs["tmdb"] = "0042" }},
		{"unsafe envelope", func(p *Page) { p.SourceID = "list:1\nsecret" }},
		{"oversized count", func(p *Page) { p.Total = 10001 }},
		{"oversized page", func(p *Page) { p.Entries = make([]Entry, 101); p.Total = 101 }},
		{"missing digest", func(p *Page) { p.Digest = Hash{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPage(t)
			tc.change(&p)
			if ValidatePageAt(p, snapshotNow) == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}

func TestListSnapshotEmptyAndContinuation(t *testing.T) {
	p := validPage(t)
	p.Entries = []Entry{}
	p.Total = 0
	p.Digest, _ = Digest(p.Entries)
	if err := ValidatePageAt(p, snapshotNow); err != nil {
		t.Fatal("valid empty snapshot", err)
	}
	p = validPage(t)
	p.Total = 2
	p.Final = false
	p.NextCursor = "next"
	if err := ValidatePageAt(p, snapshotNow); err != nil {
		t.Fatal("valid continuation", err)
	}
}

func TestListDigestCanonicalAndOrderSensitive(t *testing.T) {
	a := Entry{Kind: "movie", Title: "A", Year: 2020, ExternalIDs: map[string]string{"imdb": "tt0000042", "tmdb": "42"}}
	b := Entry{Kind: "movie", Title: "B", ExternalIDs: map[string]string{"tmdb": "43"}}
	x, err := Digest([]Entry{a})
	if err != nil {
		t.Fatal(err)
	}
	// Literal fixture pins canonical bytes for interoperable independent implementations.
	if x.String() != "7a377cc39fe1e489d55aaed7ca3beb74a08fb95b137e0a822699742a8a5e33c0" {
		t.Fatal("canonical digest changed", x.String())
	}
	a.ExternalIDs = map[string]string{"tmdb": "42", "imdb": "tt0000042"}
	y, _ := Digest([]Entry{a})
	if x != y {
		t.Fatal("map ordering changed digest")
	}
	x, _ = Digest([]Entry{a, b})
	y, _ = Digest([]Entry{b, a})
	if x == y {
		t.Fatal("source order lost")
	}
	a.ExternalIDs["tmdb"] = "invalid"
	if _, err := Digest([]Entry{a}); err == nil {
		t.Fatal("invalid digest identity accepted")
	}
}

func TestListPageStrictJSON(t *testing.T) {
	raw, err := json.Marshal(validPage(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePage(raw, snapshotNow); err != nil {
		t.Fatal(err)
	}
	for _, changed := range [][]byte{
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1),
		bytes.Replace(raw, []byte(`"tmdb":"42"`), []byte(`"tmdb":"42","tmdb":"43"`), 1),
		bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"notes":"private"`), 1),
		append(append([]byte{}, raw...), []byte(` {}`)...),
		[]byte(strings.Repeat(" ", MaxSnapshotBytes+1)),
		bytes.Replace(raw, []byte("Synthetic movie"), []byte{'b', 'a', 'd', 0xff}, 1),
	} {
		if _, err := DecodePage(changed, snapshotNow); err == nil {
			t.Fatal("malformed JSON accepted")
		}
	}
}

func TestListSnapshotAllowsNativeUnicodeCatalogTitle(t *testing.T) {
	p := validPage(t)
	p.Entries[0].Title = strings.Repeat("界", 500)
	p.Digest, _ = Digest(p.Entries)
	if err := ValidatePageAt(p, snapshotNow); err != nil {
		t.Fatal("valid native catalog title rejected", err)
	}
}

func TestListDigestSupportsExactBookKinds(t *testing.T) {
	for _, kind := range []string{"ebook", "audiobook"} {
		if _, err := Digest([]Entry{{Kind: kind, ExternalIDs: map[string]string{"isbn": "9780140328721"}}}); err != nil {
			t.Fatal(kind, err)
		}
	}
	if _, err := Digest([]Entry{{Kind: "ebook", ExternalIDs: map[string]string{"isbn": "9780140328722"}}}); err == nil {
		t.Fatal("bad ISBN checksum accepted")
	}
}

func TestListSnapshotRetainsUnsupportedSourceEntries(t *testing.T) {
	e := Entry{Kind: "unsupported", Title: "Synthetic manual title", SourceEntryID: "entry:17", UnsupportedReason: "missing_identity"}
	p := validPage(t)
	p.Entries = []Entry{e}
	var err error
	p.Digest, err = Digest(p.Entries)
	if err != nil {
		t.Fatal("unsupported source row must survive for unmatched status", err)
	}
	s, err := VerifySnapshot([]Page{p}, snapshotNow)
	if err != nil || len(s.Entries) != 1 || s.Entries[0].SourceEntryID != "entry:17" {
		t.Fatal(s, err)
	}
	e.UnsupportedReason = "arbitrary_private_text"
	if _, err := Digest([]Entry{e}); err == nil {
		t.Fatal("unbounded unsupported reason accepted")
	}
}
