package listcollections

import "testing"

func twoPages(t *testing.T) []Page {
	t.Helper()
	a := validPage(t)
	b := validPage(t)
	b.Entries[0].ExternalIDs["tmdb"] = "43"
	digest, err := Digest(append(a.Entries, b.Entries...))
	if err != nil {
		t.Fatal(err)
	}
	a.Digest = digest
	b.Digest = digest
	a.Total = 2
	b.Total = 2
	a.Final = false
	a.NextCursor = "next"
	b.Position = 1
	return []Page{a, b}
}

func TestListSnapshotRequiresCompleteConsistentPages(t *testing.T) {
	p := twoPages(t)
	s, err := VerifySnapshot(p, snapshotNow)
	if err != nil || len(s.Entries) != 2 || s.Entries[0].ExternalIDs["tmdb"] != "42" || s.Entries[1].ExternalIDs["tmdb"] != "43" {
		t.Fatal(s, err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]Page) []Page
	}{
		{"missing final", func(p []Page) []Page { return p[:1] }},
		{"duplicate page", func(p []Page) []Page { return append(p[:1], p...) }},
		{"out of order", func(p []Page) []Page { return []Page{p[1], p[0]} }},
		{"changed generation", func(p []Page) []Page { p[1].Generation++; return p }},
		{"changed source", func(p []Page) []Page { p[1].SourceID = "other"; return p }},
		{"changed snapshot", func(p []Page) []Page { p[1].SnapshotID = "other"; return p }},
		{"changed lease", func(p []Page) []Page { p[1].LeaseToken = "other"; return p }},
		{"changed digest", func(p []Page) []Page { p[1].Digest[0]++; return p }},
		{"changed entry", func(p []Page) []Page { p[1].Entries[0].ExternalIDs["tmdb"] = "44"; return p }},
		{"extra final", func(p []Page) []Page { return append(p, p[1]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := VerifySnapshot(tc.mutate(twoPages(t)), snapshotNow); err == nil {
				t.Fatal("incomplete or inconsistent snapshot accepted")
			}
		})
	}
}

func TestListSnapshotDoesNotRetainMutableTransportRows(t *testing.T) {
	pages := twoPages(t)
	s, err := VerifySnapshot(pages, snapshotNow)
	if err != nil {
		t.Fatal(err)
	}
	pages[0].Entries[0].ExternalIDs["tmdb"] = "999"
	if s.Entries[0].ExternalIDs["tmdb"] != "42" {
		t.Fatal("verified snapshot mutated")
	}
}

func TestListSnapshotAcceptsVerifiedEmpty(t *testing.T) {
	p := validPage(t)
	p.Entries = nil
	p.Total = 0
	p.Digest, _ = Digest(nil)
	s, err := VerifySnapshot([]Page{p}, snapshotNow)
	if err != nil || len(s.Entries) != 0 {
		t.Fatal(s, err)
	}
	if _, err := VerifySnapshot(nil, snapshotNow); err == nil {
		t.Fatal("absent snapshot accepted as empty")
	}
}
