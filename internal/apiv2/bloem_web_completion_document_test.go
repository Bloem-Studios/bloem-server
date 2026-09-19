package apiv2

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/ambience"
	"github.com/Silo-Server/silo-server/internal/auth"
	"github.com/Silo-Server/silo-server/internal/promotions"
	"github.com/Silo-Server/silo-server/internal/tenancy"
	"github.com/google/uuid"
)

// These are the 17 methods on the 11 paths added by the web-completion
// commit. Checking methods and wire contracts catches omissions that the
// immutable path-count ceiling alone cannot detect.
var bloemWebCompletionOperations = []struct {
	path, method, status, security string
}{
	{"/profile-credentials/{id}", "get", "200", "bloemAccountSession"},
	{"/profile-credentials/{id}", "put", "204", "bloemAccountSession"},
	{"/profile-credentials/{id}", "delete", "204", "bloemAccountSession"},
	{"/admin/organization/activity", "get", "200", "bloemOrganizationContext"},
	{"/admin/organization/invitations/{id}/resend", "post", "201", "bloemOrganizationContext"},
	{"/admin/organization/invitations/{id}", "delete", "204", "bloemOrganizationContext"},
	{"/admin/platform/promotions/", "get", "200", "bloemPlatformContext"},
	{"/admin/platform/promotions/", "post", "201", "bloemPlatformContext"},
	{"/admin/platform/promotions/{id}", "put", "200", "bloemPlatformContext"},
	{"/admin/platform/promotions/{id}", "delete", "204", "bloemPlatformContext"},
	{"/admin/platform/ambience/", "get", "200", "bloemPlatformContext"},
	{"/admin/platform/ambience/", "post", "201", "bloemPlatformContext"},
	{"/admin/platform/ambience/{id}", "put", "200", "bloemPlatformContext"},
	{"/admin/platform/ambience/{id}", "delete", "204", "bloemPlatformContext"},
	{"/admin/platform/ambience/assets", "post", "201", "bloemPlatformContext"},
	{"/admin/platform/ambience/{id}/assets", "post", "201", "bloemPlatformContext"},
	{"/ambience", "get", "200", "bearerAuth"},
}

func bloemWebDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func bloemDocObject(t *testing.T, value any, keys ...string) map[string]any {
	t.Helper()
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected object before %q, got %T", key, value)
		}
		value = object[key]
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected object at %v, got %T", keys, value)
	}
	return object
}

func bloemDocSchema(t *testing.T, doc map[string]any, schema map[string]any) map[string]any {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		return bloemDocObject(t, doc, "components", "schemas", strings.TrimPrefix(ref, "#/components/schemas/"))
	}
	return schema
}

func bloemDocFields(t *testing.T, schema map[string]any, want ...string) {
	t.Helper()
	var got []string
	for key := range bloemDocObject(t, schema, "properties") {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestBloemWebCompletionDocumentOperations(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range bloemWebCompletionOperations {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
			wantSecurity := []any{map[string]any{tc.security: []any{}}}
			if !reflect.DeepEqual(op["security"], wantSecurity) {
				t.Errorf("security = %v, want %v", op["security"], wantSecurity)
			}
			scheme := bloemDocObject(t, doc, "components", "securitySchemes", tc.security)
			if scheme["type"] != "http" || scheme["scheme"] != "bearer" {
				t.Errorf("expected bearer scheme, got %v", scheme)
			}
			responses := bloemDocObject(t, op, "responses")
			response := bloemDocObject(t, responses, tc.status)
			for status := range responses {
				if strings.HasPrefix(status, "2") && status != tc.status {
					t.Errorf("unexpected success status %s", status)
				}
			}
			if tc.status == "204" {
				if _, ok := response["content"]; ok {
					t.Error("204 must be bodyless")
				}
			} else {
				schema := bloemDocSchema(t, doc, bloemDocObject(t, response, "content", "application/json", "schema"))
				if len(bloemDocObject(t, schema, "properties")) == 0 {
					t.Error("success must describe the handler's typed response")
				}
			}
			for _, status := range []string{"401", "403", "503"} {
				response := bloemDocObject(t, responses, status)
				schema := bloemDocSchema(t, doc, bloemDocObject(t, response, "content", "application/json", "schema"))
				bloemDocFields(t, schema, "error", "message")
				if _, ok := bloemDocObject(t, response, "content")["application/problem+json"]; ok {
					t.Error("chi handlers do not emit v2 Problem responses")
				}
			}
		})
	}
}

func TestBloemWebCompletionDocumentRevisionsAndEnvelopes(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		path, method, status string
		fields               []string
	}{
		{"/profile-credentials/{id}", "get", "200", []string{"profile_id", "login_email", "configured", "credential_revision"}},
		{"/admin/organization/activity", "get", "200", []string{"events", "next_cursor"}},
		{"/admin/organization/invitations/{id}/resend", "post", "201", []string{"invitation", "claim_token"}},
		{"/admin/platform/promotions/", "get", "200", []string{"promotions", "surfaces"}},
		{"/admin/platform/ambience/", "get", "200", []string{"packs", "storage_available", "yearly_scheduling"}},
		{"/admin/platform/ambience/assets", "post", "201", []string{"url", "asset"}},
		{"/admin/platform/ambience/{id}/assets", "post", "201", []string{"url", "slot", "pack"}},
		{"/ambience", "get", "200", []string{"ambience"}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			schema := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method, "responses", tc.status, "content", "application/json", "schema")
			bloemDocFields(t, bloemDocSchema(t, doc, schema), tc.fields...)
		})
	}
	for _, tc := range []struct{ path, method, revision string }{
		{"/profile-credentials/{id}", "put", "credential revision"},
		{"/profile-credentials/{id}", "delete", "credential revision"},
		{"/admin/organization/invitations/{id}/resend", "post", "policy revision"},
		{"/admin/organization/invitations/{id}", "delete", "policy revision"},
	} {
		t.Run(tc.method+tc.path+" revision", func(t *testing.T) {
			op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
			schema := bloemDocSchema(t, doc, bloemDocObject(t, op, "requestBody", "content", "application/json", "schema"))
			revision := bloemDocObject(t, schema, "properties", "expected_revision")
			if revision["type"] != "integer" || revision["minimum"] != float64(1) || !strings.Contains(revision["description"].(string), tc.revision) {
				t.Errorf("incorrect revision contract: %v", revision)
			}
			found := false
			for _, field := range schema["required"].([]any) {
				found = found || field == "expected_revision"
			}
			if !found {
				t.Error("expected_revision must be required")
			}
			for _, status := range []string{"400", "409", "422"} {
				bloemDocObject(t, op, "responses", status)
			}
		})
	}
}

func TestBloemWebCompletionDocumentArtifact(t *testing.T) {
	raw, err := GenerateBloemOpenAPI()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../contracts/api/bloem/v1/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(committed) {
		t.Fatal("native OpenAPI artifact is stale; run make bloem-openapi")
	}
}

func TestBloemWebCompletionDocumentInputsAndTokens(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		path     string
		required []any
	}{
		{"/admin/platform/promotions/", []any{"surfaces", "headline", "image_url", "starts_at", "ends_at"}},
		{"/admin/platform/ambience/", []any{"effect_id", "window"}},
	} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, "post")
		schema := bloemDocSchema(t, doc, bloemDocObject(t, op, "requestBody", "content", "application/json", "schema"))
		if !reflect.DeepEqual(schema["required"], tc.required) {
			t.Errorf("%s required=%v, want %v; defaulted fields must remain optional", tc.path, schema["required"], tc.required)
		}
		if _, ok := bloemDocObject(t, op, "responses")["422"]; ok {
			t.Errorf("%s validates with 400, not v2's 422", tc.path)
		}
	}
	for _, tc := range []struct {
		path   string
		fields []string
	}{
		{"/admin/platform/ambience/assets", []string{"file", "asset_id", "kind", "checksum", "content_type"}},
		{"/admin/platform/ambience/{id}/assets", []string{"file", "slot"}},
	} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, "post")
		body := bloemDocObject(t, op, "requestBody")
		media := bloemDocObject(t, body, "content")
		if body["required"] != true || len(media) != 1 {
			t.Errorf("%s must require only multipart/form-data: %v", tc.path, body)
		}
		schema := bloemDocSchema(t, doc, bloemDocObject(t, media, "multipart/form-data", "schema"))
		bloemDocFields(t, schema, tc.fields...)
		if !reflect.DeepEqual(schema["required"], []any{"file"}) {
			t.Errorf("%s must require only file, got %v", tc.path, schema["required"])
		}
		file := bloemDocObject(t, schema, "properties", "file")
		if file["type"] != "string" || file["contentEncoding"] != "binary" {
			t.Errorf("file must be binary: %v", file)
		}
		for _, status := range []string{"400", "413", "415", "500", "503"} {
			bloemDocObject(t, op, "responses", status)
		}
	}
	resend := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/invitations/{id}/resend", "post")
	response := bloemDocSchema(t, doc, bloemDocObject(t, resend, "responses", "201", "content", "application/json", "schema"))
	token := bloemDocObject(t, response, "properties", "claim_token")
	if token["type"] != "string" || token["readOnly"] != true || !strings.Contains(token["description"].(string), "only its hash") {
		t.Errorf("claim token must describe one-time disclosure and hash-only storage: %v", token)
	}
	if !strings.Contains(resend["description"].(string), "does not send email") || !strings.Contains(resend["description"].(string), "Do not automatically retry") {
		t.Error("resend must distinguish rotation from email delivery and receipt replay")
	}
	invitation := bloemDocSchema(t, doc, bloemDocObject(t, response, "properties", "invitation"))
	bloemDocFields(t, invitation, "id", "email", "role", "access_group_id", "library_ids", "create_profile", "show_tour", "note", "invited_by", "invited_by_name", "status", "expires_at", "accepted_at", "accepted_user_id", "created_at")
	for _, method := range []string{"put", "delete"} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+"/profile-credentials/{id}", method)
		schema := bloemDocSchema(t, doc, bloemDocObject(t, op, "requestBody", "content", "application/json", "schema"))
		for _, field := range []string{"current_password", "password"} {
			if bloemDocObject(t, schema, "properties", field)["writeOnly"] != true {
				t.Errorf("%s %s must be writeOnly", method, field)
			}
		}
	}
}

func TestBloemWebCompletionDocumentAuthorityHeaders(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range bloemWebCompletionOperations {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
		params, _ := op["parameters"].([]any)
		headers := map[string]map[string]any{}
		for _, raw := range params {
			param := bloemDocObject(t, raw)
			if param["in"] == "header" {
				headers[param["name"].(string)] = param
			}
		}
		for _, forbidden := range []string{"If-Match", "If-None-Match", "Idempotency-Key"} {
			if _, ok := headers[forbidden]; ok {
				t.Errorf("%s %s must not advertise unsupported %s", tc.method, tc.path, forbidden)
			}
		}
		switch tc.security {
		case "bloemAccountSession":
			if headers["X-Profile-Id"] == nil || headers["X-Profile-Id"]["required"] == true || headers["X-Profile-Token"] == nil {
				t.Errorf("%s must document conditional household primary/PIN headers", tc.method)
			}
		case "bearerAuth":
			if headers["X-Profile-Id"]["required"] != true || headers["X-Profile-Token"] == nil {
				t.Error("seasonal viewer delivery requires the acting profile and documents PIN proof")
			}
			if !strings.Contains(op["description"].(string), "excludes direct-profile sessions") {
				t.Error("seasonal delivery must retain the direct-profile boundary")
			}
			bloemDocObject(t, op, "responses", "200", "headers", "Cache-Control")
		default:
			if len(headers) != 0 {
				t.Errorf("%s must derive admin scope from its context token, got headers %v", tc.path, headers)
			}
		}
	}
}

// Compare the generated document recursively with the actual domain DTOs used
// by the handlers. This catches drift in nested campaign, pack, targeting and
// audit fields even when the path and top-level envelope stay unchanged.
func TestBloemWebCompletionDocumentDomainShapes(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		schema string
		wire   any
	}{
		{"BloemPromotionBody", promotions.Input{}},
		{"BloemAmbienceBody", ambience.Input{}},
		{"Promotion", promotions.Promotion{}},
		{"Pack", ambience.Pack{}},
		{"Wire", ambience.Wire{}},
		{"StoredAsset", ambience.StoredAsset{}},
		{"ProfileCredentialStatus", auth.ProfileCredentialStatus{}},
		{"OrganizationAuditPage", tenancy.OrganizationAuditPage{}},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			bloemDocMatchesType(t, doc, bloemDocObject(t, doc, "components", "schemas", tc.schema), reflect.TypeOf(tc.wire))
		})
	}
}

func bloemDocMatchesType(t *testing.T, doc, schema map[string]any, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// Compare the value branch of a nullable reference. Actual null acceptance
	// is checked against marshaled JSON in the engagement document tests.
	if variants, ok := schema["anyOf"].([]any); ok {
		var valueSchema map[string]any
		for _, variant := range variants {
			branch := bloemDocObject(t, variant)
			if branch["type"] == "null" {
				continue
			}
			if valueSchema != nil {
				t.Fatal("shape comparison requires exactly one non-null schema branch")
			}
			valueSchema = branch
		}
		if valueSchema == nil {
			t.Fatal("nullable schema has no value branch")
		}
		schema = valueSchema
	}
	schema = bloemDocSchema(t, doc, schema)
	wantType := ""
	switch {
	case typ == reflect.TypeFor[time.Time]() || typ == reflect.TypeFor[uuid.UUID]():
		wantType = "string"
	case typ.Kind() == reflect.Struct:
		wantType = "object"
		var names []string
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if !field.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			names = append(names, name)
			bloemDocMatchesType(t, doc, bloemDocObject(t, schema, "properties", name), field.Type)
		}
		bloemDocFields(t, schema, names...)
	case typ.Kind() == reflect.Slice:
		wantType = "array"
		bloemDocMatchesType(t, doc, bloemDocObject(t, schema, "items"), typ.Elem())
	case typ.Kind() == reflect.Int || typ.Kind() == reflect.Int64:
		wantType = "integer"
	case typ.Kind() == reflect.Float64:
		wantType = "number"
	case typ.Kind() == reflect.Bool:
		wantType = "boolean"
	case typ.Kind() == reflect.String:
		wantType = "string"
	default:
		t.Fatalf("unhandled domain type %s", typ)
	}
	actual := schema["type"]
	if variants, ok := actual.([]any); ok {
		for _, variant := range variants {
			if variant != "null" {
				actual = variant
			}
		}
	}
	if actual != wantType {
		t.Errorf("%s schema type=%v, want %s", typ, schema["type"], wantType)
	}
}
