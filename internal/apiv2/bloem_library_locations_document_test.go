package apiv2

import (
	"encoding/json"
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
	for _, scope := range []string{"platform", "organization"} {
		base := BloemPrefix + "/admin/" + scope
		for _, op := range []struct{ method, path, status string }{{"post", "/library-locations", "201"}, {"get", "/library-locations/{library_id}", "200"}, {"put", "/library-locations/{library_id}", "200"}, {"get", "/native-storage/sources/{source_key}/browse", "200"}} {
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
		}
	}
}
