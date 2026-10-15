package apiv2

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/nativestorage"
	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Hand-checked against the mounted chi contract. Missing a scope or accidentally
// applying v2 authority/conditional-write semantics must break the generated API.
func TestBloemNativeStorageDocumentOperations(t *testing.T) {
	doc := bloemWebDocument(t)
	routes := []struct{ method, path, status string }{
		{"get", "/capabilities", "200"}, {"get", "/artifacts", "200"},
		{"get", "/sources", "200"}, {"get", "/sources/{source_key}", "200"},
		{"get", "/sources/{source_key}/browse", "200"},
		{"post", "/installations", "201"}, {"post", "/installations/discover", "201"}, {"put", "/sources/{source_key}/configuration", "200"},
		{"post", "/installations/{installation_id}/disable", "200"}, {"delete", "/installations/{installation_id}", "200"},
		{"post", "/installations/{installation_id}/upgrade", "200"},
		{"post", "/catalog/installations", "201"}, {"post", "/catalog/installations/{installation_id}/upgrade", "200"},
	}
	count := 0
	for path, value := range bloemDocObject(t, doc, "paths") {
		if strings.Contains(path, "/native-storage/") {
			count += len(value.(map[string]any))
		}
	}
	if count != 2*len(routes) {
		t.Errorf("native operations = %d, want %d mounted methods", count, 2*len(routes))
	}
	for _, scope := range []string{"platform", "organization"} {
		security := "bloemPlatformContext"
		if scope == "organization" {
			security = "bloemOrganizationContext"
		}
		for _, route := range routes {
			t.Run(scope+route.method+route.path, func(t *testing.T) {
				op := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/"+scope+"/native-storage"+route.path, route.method)
				if want := []any{map[string]any{security: []any{}}}; !reflect.DeepEqual(op["security"], want) {
					t.Errorf("security = %v", op["security"])
				}
				for _, parameter := range anySlice(op["parameters"]) {
					p := parameter.(map[string]any)
					if p["in"] == "header" {
						t.Errorf("invented header parameter: %v", p)
					}
				}
				for status, value := range bloemDocObject(t, op, "responses") {
					response := value.(map[string]any)
					if strings.HasPrefix(status, "2") && status != route.status {
						t.Errorf("unexpected success %s", status)
					}
					if _, problem := bloemDocObject(t, response, "content")["application/problem+json"]; problem {
						t.Error("native chi response is not a v2 Problem")
					}
					if status == route.status {
						header := bloemDocObject(t, response, "headers", "Cache-Control", "schema")
						if !reflect.DeepEqual(header["enum"], []any{"no-store"}) {
							t.Errorf("cache policy = %v", header)
						}
					}
				}
				if route.method == "get" && op["requestBody"] != nil {
					t.Error("GET does not accept a body")
				}
				if route.path == "/installations" || route.path == "/installations/discover" {
					form := bloemDocObject(t, op, "requestBody", "content", "multipart/form-data")
					schema := bloemDocSchema(t, doc, bloemDocObject(t, form, "schema"))
					bloemDocFields(t, schema, "request", "binary")
					if schema["additionalProperties"] != false {
						t.Error("multipart extra fields must be rejected")
					}
					if !reflect.DeepEqual(schema["required"], []any{"request", "binary"}) {
						t.Errorf("multipart required = %v", schema["required"])
					}
					if bloemDocObject(t, form, "encoding", "request")["contentType"] != "application/json" {
						t.Error("request part must document JSON")
					}
					request := bloemDocSchema(t, doc, bloemDocObject(t, schema, "properties", "request"))
					_, org := bloemDocObject(t, request, "properties")["organization_id"]
					if org != (scope == "platform") {
						t.Errorf("organization_id allowed in %s = %v", scope, org)
					}
				}
			})
		}
	}
}

func TestBloemNativeStorageDiscoveryDocumentRequestBoundaries(t *testing.T) {
	doc := bloemWebDocument(t)
	compiler := jsonschema.NewCompiler()
	const resource = "https://schema.example.invalid/bloem-storage-discovery.json"
	if err := compiler.AddResource(resource, doc); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"platform", "organization"} {
		t.Run(scope, func(t *testing.T) {
			op := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/"+scope+"/native-storage/installations/discover", "post")
			form := bloemDocObject(t, op, "requestBody", "content", "multipart/form-data", "schema", "properties", "request")
			schema, err := compiler.Compile(resource + form["$ref"].(string))
			if err != nil {
				t.Fatal(err)
			}
			valid := map[string]any{"artifact_key": "approved-s3", "provider_source_id": " opaque/../source ", "enabled": false, "config": map[string]any{"connection": map[string]any{"bucket": "fixture"}}}
			if err = schema.Validate(valid); err != nil {
				t.Fatalf("valid discovery schema rejected: %v", err)
			}
			for _, field := range []string{"artifact_key", "provider_source_id", "enabled", "config"} {
				invalid := map[string]any{}
				for key, value := range valid {
					if key != field {
						invalid[key] = value
					}
				}
				if schema.Validate(invalid) == nil {
					t.Errorf("missing %s accepted", field)
				}
				invalid[field] = nil
				if schema.Validate(invalid) == nil {
					t.Errorf("null %s accepted", field)
				}
			}
			for _, field := range []string{"root_entry_id", "source_key", "expected_revision"} {
				valid[field] = nil
				if schema.Validate(valid) == nil {
					t.Errorf("caller %s accepted", field)
				}
				delete(valid, field)
			}
			valid["organization_id"] = nil
			if got := schema.Validate(valid) == nil; got != (scope == "platform") {
				t.Errorf("organization null accepted=%v for %s scope", got, scope)
			}
			delete(valid, "organization_id")
			valid["config"] = map[string]any{"connection": nil}
			if schema.Validate(valid) == nil {
				t.Error("null configuration namespace accepted")
			}
			old := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/"+scope+"/native-storage/installations", "post", "requestBody", "content", "multipart/form-data", "schema", "properties", "request")
			oldSchema, err := compiler.Compile(resource + old["$ref"].(string))
			if err != nil {
				t.Fatal(err)
			}
			valid["config"] = map[string]any{}
			if oldSchema.Validate(valid) == nil {
				t.Fatal("old install schema no longer requires a root")
			}
		})
	}
}

func anySlice(value any) []any { result, _ := value.([]any); return result }

// Nil UUID/int fields are emitted as JSON null by the real domain types;
// documenting them as strings or omitting them breaks strict generated clients.
func TestBloemNativeStorageDocumentNullableResponses(t *testing.T) {
	doc := bloemWebDocument(t)
	key := uuid.MustParse("19b3c8a5-44e4-4334-8133-a2f8b8ee0f90")
	source := nativestorage.SourceView{SourceKey: key, OwnerKind: "platform", PluginID: "storage", ProviderSourceID: "books", RootEntryID: "root", ConfigurationRevision: 1, State: "detached", ConnectionSummary: map[string]string{}}
	for _, tc := range []struct {
		path string
		body any
	}{
		{"/sources/{source_key}", map[string]any{"source": source}},
		{"/sources", nativestorage.SourcePage{Sources: []nativestorage.SourceView{source}}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			schema := bloemEngagementSchema(t, doc, "/admin/platform/native-storage"+tc.path, "get", "200")
			if err := schema.Validate(bloemEngagementJSON(t, tc.body)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBloemNativeStorageDocumentRequestBoundaries(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, scope := range []string{"platform", "organization"} {
		path := "/admin/" + scope + "/native-storage"
		t.Run(scope, func(t *testing.T) {
			configuration := bloemEngagementSchema(t, doc, path+"/sources/{source_key}/configuration", "put", "")
			valid := map[string]any{"expected_revision": 1, "config": map[string]any{}}
			if err := configuration.Validate(valid); err != nil {
				t.Fatal(err)
			}
			for _, config := range []any{nil, []any{}, map[string]any{"provider": nil}, map[string]any{"provider": "secret"}} {
				invalid := map[string]any{"expected_revision": 1, "config": config}
				if err := configuration.Validate(invalid); err == nil {
					t.Errorf("invalid configuration accepted: %v", config)
				}
			}
			valid["expected_revision"] = 0
			if err := configuration.Validate(valid); err == nil {
				t.Error("source revision must be positive")
			}
			failure := bloemEngagementSchema(t, doc, path+"/sources/{source_key}/configuration", "put", "503")
			unknown := map[string]any{"error": "mutation_outcome_unknown", "message": "Mutation outcome requires reconciliation", "operation": "configuration", "operation_id": "19b3c8a5-44e4-4334-8133-a2f8b8ee0f90", "source_key": "19b3c8a5-44e4-4334-8133-a2f8b8ee0f90"}
			if err := failure.Validate(unknown); err != nil {
				t.Fatal(err)
			}
			unknown["source_key"] = nil
			if err := failure.Validate(unknown); err == nil {
				t.Error("unknown recovery identifiers must be omitted, not null")
			}
		})
	}
}

func TestBloemNativeStorageDiscoveryDocumentArtifactRejection(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, scope := range []string{"platform", "organization"} {
		t.Run(scope, func(t *testing.T) {
			schema := bloemEngagementSchema(t, doc, "/admin/"+scope+"/native-storage/installations/discover", "post", "422")
			body := map[string]any{"error": "artifact_rejected", "message": "Uploaded artifact rejected"}
			if err := schema.Validate(body); err != nil {
				t.Fatalf("artifact rejection envelope does not match the declared response: %v", err)
			}
		})
	}
}
