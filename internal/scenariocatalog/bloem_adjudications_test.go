package scenariocatalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBloemAdjudicationsPreserveFrozenCatalogs(t *testing.T) {
	originals, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(originals)
	if err != nil {
		t.Fatal(err)
	}
	current, err := BloemCurrentCatalogs(originals)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(originals)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("adjudication modified the frozen input")
	}
	changed := 0
	for ci, c := range current {
		for ri, row := range c.Rows {
			for si, s := range row.Scenarios {
				old := originals[ci].Rows[ri].Scenarios[si]
				if !sameSequenceShape(s.Expect, old.Expect) {
					changed++
				}
				s.Expect = old.Expect
				if s.V2Expectation != nil {
					if !sameSequenceShape(s.V2Expectation, old.V2Expectation) {
						changed++
					}
					pair := *s.V2Expectation
					pair.Expect = old.V2Expectation.Expect
					pair.Kind = old.V2Expectation.Kind
					pair.Summary = old.V2Expectation.Summary
					pair.RecordedIn = old.V2Expectation.RecordedIn
					for i := range pair.Then {
						pair.Then[i].Expect = old.V2Expectation.Then[i].Expect
					}
					s.V2Expectation = &pair
				}
				if s.Method() != old.Method() || !sameSequenceShape(s, old) {
					t.Fatalf("changed execution rather than expectations: %s", s.ID)
				}
			}
		}
	}
	if changed != 49 {
		t.Fatalf("reviewed transport count = %d, want 49", changed)
	}
}

func TestBloemAdjudicationsRefuseChangedOriginal(t *testing.T) {
	for _, field := range []string{"expectation", "request", "principal", "row"} {
		t.Run(field, func(t *testing.T) {
			catalogs, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range catalogs {
				for ri := range c.Rows {
					row := &c.Rows[ri]
					for si := range row.Scenarios {
						s := &row.Scenarios[si]
						if s.ID != "keys_list.meaning" {
							continue
						}
						found = true
						switch field {
						case "expectation":
							s.Expect.Status = 418
						case "request":
							s.Request.Path += "/changed"
						case "principal":
							s.Principal.Class = "public"
						case "row":
							row.RegistrationIndex++
						}
					}
				}
			}
			if !found {
				t.Fatal("missing frozen scenario")
			}
			if _, err := BloemCurrentCatalogs(catalogs); err == nil {
				t.Fatal("accepted drift in adjudicated original")
			}
		})
	}
}

func TestBloemUpdateAdjudicationPinsRewritesOnlyChangedDigests(t *testing.T) {
	digest := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	fsys := fstest.MapFS{
		"api/a.json": {Data: []byte("new bytes")},
		"api/b.json": {Data: []byte("same bytes")},
	}
	stale := strings.Repeat("0", 64)
	raw := `{
  "schema_version": 1,
  "catalog_sha256": {
    "api/a.json": "` + stale + `",
    "api/b.json": "` + digest("same bytes") + `"
  },
  "decisions": []
}
`
	got, err := bloemUpdateAdjudicationPins([]byte(raw), fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(raw, stale, digest("new bytes"), 1)
	if string(got) != want {
		t.Fatalf("pins:\n%s\nwant:\n%s", got, want)
	}
}
