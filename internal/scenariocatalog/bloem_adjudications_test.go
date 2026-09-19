package scenariocatalog

import (
	"encoding/json"
	"testing"
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
	if changed != 30 {
		t.Fatalf("reviewed transport count = %d, want 30", changed)
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
