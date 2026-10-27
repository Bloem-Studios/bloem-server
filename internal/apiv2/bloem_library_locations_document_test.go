package apiv2

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestBloemLibraryLocationsDocumentOperations(t *testing.T) {
	raw, err := GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Headers map[string]struct {
					Schema struct {
						Enum []string `json:"enum"`
					} `json:"schema"`
				} `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// Each list is what the handler, its domain calls and the administrative
	// middleware can write: no 413 without a body, and provider pages (502)
	// are only read by the browser.
	for _, scope := range []string{"platform", "organization"} {
		base := BloemPrefix + "/admin/" + scope
		for _, op := range []struct {
			method, path, status string
			statuses             []string
		}{
			{"post", "/library-locations", "201", []string{"201", "400", "401", "403", "404", "409", "413", "422", "500", "503"}},
			{"get", "/library-locations/{library_id}", "200", []string{"200", "400", "401", "403", "404", "409", "503"}},
			{"put", "/library-locations/{library_id}", "200", []string{"200", "400", "401", "403", "404", "409", "413", "422", "503"}},
			{"get", "/native-storage/sources/{source_key}/browse", "200", []string{"200", "400", "401", "403", "404", "409", "422", "502", "503"}},
		} {
			operation, ok := doc.Paths[base+op.path][op.method]
			if !ok {
				t.Errorf("missing %s %s", op.method, base+op.path)
				continue
			}
			// The handlers write every response, success included, with no-store.
			for status, response := range operation.Responses {
				if enum := response.Headers["Cache-Control"].Schema.Enum; len(enum) != 1 || enum[0] != "no-store" {
					t.Errorf("%s %s %s documents Cache-Control %v", op.method, base+op.path, status, enum)
				}
			}
			if _, ok := operation.Responses[op.status]; !ok {
				t.Errorf("%s %s lacks success %s", op.method, base+op.path, op.status)
			}
			statuses := make([]string, 0, len(operation.Responses))
			for status := range operation.Responses {
				statuses = append(statuses, status)
			}
			slices.Sort(statuses)
			if !reflect.DeepEqual(statuses, op.statuses) {
				t.Errorf("%s %s documents %v, want %v", op.method, base+op.path, statuses, op.statuses)
			}
		}
	}
}
