package scenariocatalog

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Silo-Server/silo-server/contracts/api/v2/scenarios"
)

func TestRequiredProfilePairingCannotShrink(t *testing.T) {
	for _, remove := range []bool{false, true} {
		catalogs, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		pilot, err := ProfileListAcceptance(catalogs)
		if err != nil {
			t.Fatal(err)
		}
		if remove {
			pilot[0].Rows[0].Scenarios = pilot[0].Rows[0].Scenarios[1:]
		} else {
			pilot[0].Rows[0].Scenarios[0].V2Expectation = nil
		}
		if _, err := ProfileListAcceptance(pilot); err == nil || !strings.Contains(err.Error(), "profiles_list.ok") {
			t.Fatalf("missing pairing accepted: %v", err)
		}
	}
}

func TestPairingSchemaAndOperation(t *testing.T) {
	for _, field := range []string{"method", "request", "expect", "operation_id", "headers", "body", "wrong_operation", "wrong_method", "wrong_path"} {
		t.Run(field, func(t *testing.T) {
			schema, _ := scenarios.FS.ReadFile(scenarios.SchemaPath)
			raw, _ := scenarios.FS.ReadFile("api/api-v1-profiles.json")
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			row := doc["rows"].([]any)[0].(map[string]any)
			pair := row["scenarios"].([]any)[0].(map[string]any)["v2_expectation"].(map[string]any)
			switch field {
			case "headers", "body":
				delete(pair["expect"].(map[string]any), field)
			case "wrong_operation":
				pair["operation_id"] = "deleteProfile"
			case "wrong_method":
				pair["method"] = "DELETE"
			case "wrong_path":
				pair["request"].(map[string]any)["path"] = "/api/v2/profiles/other"
			default:
				delete(pair, field)
			}
			raw, _ = json.Marshal(doc)
			_, err := load(fstest.MapFS{scenarios.SchemaPath: {Data: schema}, "api/api-v1-profiles.json": {Data: raw}})
			if err == nil {
				t.Fatalf("accepted invalid %s", field)
			}
		})
	}
}
