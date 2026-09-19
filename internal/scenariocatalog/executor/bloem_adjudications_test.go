package executor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/scenariocatalog"
)

// This focused packet runs the reviewed cases AND their unchanged transport
// partners, including every original follow-up. The ordinary RunAll gate uses
// the same decision loader; the upstream frozen catalogs/selectors stay intact.
func TestBloemAdjudicatedScenarios(t *testing.T) {
	originals, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	current, err := scenariocatalog.BloemCurrentCatalogs(originals)
	if err != nil {
		t.Fatal(err)
	}
	var selected []*scenariocatalog.Catalog
	for ci, catalog := range current {
		copyCatalog := *catalog
		copyCatalog.Rows = nil
		for ri, row := range catalog.Rows {
			copyRow := row
			copyRow.Scenarios = nil
			for si, scenario := range row.Scenarios {
				before, err := json.Marshal(originals[ci].Rows[ri].Scenarios[si])
				if err != nil {
					t.Fatal(err)
				}
				after, err := json.Marshal(scenario)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					copyRow.Scenarios = append(copyRow.Scenarios, scenario)
				}
			}
			if len(copyRow.Scenarios) > 0 {
				copyCatalog.Rows = append(copyCatalog.Rows, copyRow)
			}
		}
		if len(copyCatalog.Rows) > 0 {
			selected = append(selected, &copyCatalog)
		}
	}
	env := New(t)
	if !env.HasDatabase() {
		t.Skip(DatabaseEnv + " is required for reviewed transport acceptance")
	}
	results := runAll(t, selected, env)
	if len(results) != 52 {
		t.Fatalf("reviewed packet executed %d transport leaves, want 52", len(results))
	}
	for _, result := range results {
		if !result.Passed() {
			t.Errorf("reviewed scenario did not pass: %s/%s", result.Scenario, result.Transport)
		}
	}
}

func TestBloemAdjudicationRejectsAPIKeyDisclosure(t *testing.T) {
	originals, err := scenariocatalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	current, err := scenariocatalog.BloemCurrentCatalogs(originals)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range current {
		for _, row := range c.Rows {
			for _, scenario := range row.Scenarios {
				if scenario.ID != "keys_list.meaning" && scenario.ID != "keys_list.shape" {
					continue
				}
				for _, assertion := range scenario.Expect.Body {
					if assertion.Op != "every" || !strings.Contains(string(assertion.Value), `"absent"`) {
						continue
					}
					checked++
					if failure := checkBody(assertion, []any{map[string]any{}}, true); failure != "" {
						t.Fatal(failure)
					}
					for _, secret := range []any{nil, "", "sa_" + strings.Repeat("a", 64)} {
						if failure := checkBody(assertion, []any{map[string]any{"key": secret}}, true); failure == "" {
							t.Fatalf("%s accepted a disclosed key", scenario.ID)
						}
					}
				}
			}
		}
	}
	if checked < 2 {
		t.Fatal("both reviewed list scenarios must enforce explicit secret absence")
	}
}
