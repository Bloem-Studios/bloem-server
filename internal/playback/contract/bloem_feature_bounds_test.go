package contract

// Bloem-owned contract coverage: feature tokens in decision and capability
// responses are 1 to 128 characters, and negotiated_client_features is a set.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestFeatureTokenBoundsInResponseSchemas(t *testing.T) {
	schemas := compileSchemasV3(t)
	cases := []struct {
		golden, schema, field string
	}{
		{"decision_response.json", "decision-response.schema.json", "server_features"},
		{"decision_response.json", "decision-response.schema.json", "negotiated_client_features"},
		{"capability_response.json", "capability-response.schema.json", "features"},
	}
	for _, tc := range cases {
		t.Run(tc.schema+"/"+tc.field, func(t *testing.T) {
			for _, bad := range []string{"", strings.Repeat("f", 129)} {
				if err := validateWithExtraToken(t, schemas[tc.schema], tc.golden, tc.field, bad); err == nil {
					t.Fatalf("token of length %d accepted, want rejection", len(bad))
				}
			}
			if err := validateWithExtraToken(t, schemas[tc.schema], tc.golden, tc.field, strings.Repeat("f", 128)); err != nil {
				t.Fatalf("token of length 128 rejected: %v", err)
			}
		})
	}
}

func TestNegotiatedClientFeaturesAreUnique(t *testing.T) {
	schemas := compileSchemasV3(t)
	body := goldenObject(t, "decision_response.json")
	features := body["negotiated_client_features"].([]any)
	body["negotiated_client_features"] = append(features, features[0])
	if err := validateObject(t, schemas["decision-response.schema.json"], body); err == nil {
		t.Fatal("duplicate negotiated_client_features accepted, want rejection")
	}
}

func validateWithExtraToken(t *testing.T, schema *jsonschema.Schema, golden, field, token string) error {
	t.Helper()
	body := goldenObject(t, golden)
	list, ok := body[field].([]any)
	if !ok {
		t.Fatalf("%s has no %s list", golden, field)
	}
	body[field] = append(list, token)
	return validateObject(t, schema, body)
}

func goldenObject(t *testing.T, name string) map[string]any {
	t.Helper()
	return mustReadObject(t, filepath.Join(goldenRootV3, name))
}

func validateObject(t *testing.T, schema *jsonschema.Schema, body map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return schema.Validate(instance)
}
