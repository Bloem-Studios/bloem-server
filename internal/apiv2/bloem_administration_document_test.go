package apiv2

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Silo-Server/silo-server/internal/adminpeople"
	"github.com/Silo-Server/silo-server/internal/entitlements"
	"github.com/Silo-Server/silo-server/internal/livetv"
	"github.com/Silo-Server/silo-server/internal/livetv/gracenote"
	"github.com/Silo-Server/silo-server/internal/livetv/schedulesdirect"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestBloemAdministrationAuthorityAndPreconditions(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		path, method, scheme string
		alternatives         int
	}{
		{"/admin/session", "post", "bloemAdminLoginSession", 1},
		{"/admin/organization/groups/", "post", "bloemOrganizationContext", 1},
		{"/admin/platform/organizations/", "post", "bloemPlatformContext", 1},
		{"/admin/platform/entitlement-templates", "post", "bloemPlatformContext", 1},
		{"/admin/platform/entitlement-templates", "get", "bloemEntitlementAccountsKey", 2},
		{"/admin/platform/accounts/{account_id}/entitlement", "get", "bloemEntitlementBulkKey", 2},
		{"/admin/platform/accounts/{account_id}/entitlement/apply", "post", "bloemEntitlementAccountsKey", 2},
		{"/admin/platform/users/{user_id}/entitlement/apply", "post", "bloemPlatformContext", 1},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
			security := anySlice(op["security"])
			if len(security) != tc.alternatives {
				t.Errorf("security alternatives = %v", security)
			}
			found := false
			for _, value := range security {
				if _, ok := value.(map[string]any)[tc.scheme]; ok {
					found = true
				}
			}
			if !found {
				t.Errorf("missing security %s", tc.scheme)
			}
			for _, parameter := range anySlice(op["parameters"]) {
				p := parameter.(map[string]any)
				if p["name"] == "If-Match" {
					t.Error("native JSON revisions must not acquire v2 If-Match")
				}
			}
			for _, value := range bloemDocObject(t, op, "responses") {
				response := value.(map[string]any)
				if content, ok := response["content"].(map[string]any); ok && content["application/problem+json"] != nil {
					t.Error("chi handler must not advertise v2 Problems")
				}
			}
		})
	}
}

// Statuses that the handlers or their middleware write, or can never write,
// regardless of the shared helpers' defaults.
func TestBloemAdministrationDocumentedStatusesMatchHandlers(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct {
		method, path    string
		present, absent []string
	}{
		{"get", "/admin/organization/overview", []string{"404"}, nil},
		{"post", "/admin/organization/people/bulk-jobs", []string{"404", "409", "422"}, nil},
		{"post", "/admin/organization/people/policy-previews", []string{"404", "409"}, nil},
		{"post", "/admin/organization/people/policy-jobs", []string{"404", "409"}, nil},
		{"post", "/admin/organization/people/selections", []string{"422"}, []string{"404", "409"}},
		{"post", "/admin/platform/entitlement-templates/{key}/archive", []string{"404", "409"}, []string{"422"}},
		{"post", "/admin/platform/accounts/{account_id}/entitlement/dry-run", []string{"404", "422"}, []string{"409"}},
		{"post", "/admin/platform/users/{user_id}/entitlement/dry-run", []string{"404", "422"}, []string{"409"}},
		{"post", "/admin/platform/organizations/{id}/entitlement/dry-run", []string{"409"}, nil},
		{"post", "/admin/platform/accounts/entitlement-bulk/policy-previews", []string{"404", "409"}, []string{"429"}},
		{"post", "/admin/platform/accounts/entitlement-bulk/policy-jobs", []string{"404", "409"}, []string{"429"}},
		{"get", "/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}", []string{"404"}, []string{"409", "429"}},
		{"post", "/admin/platform/accounts/entitlement-bulk/policy-jobs/{job_id}/cancel", []string{"400", "409"}, []string{"422", "429"}},
		{"post", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-previews", []string{"404", "409"}, []string{"429"}},
		{"post", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs", []string{"404", "409"}, []string{"429"}},
		{"get", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}", []string{"404"}, []string{"409", "429"}},
		{"post", "/admin/platform/organizations/{organization_id}/entitlement-bulk/policy-jobs/{job_id}/cancel", []string{"400", "409"}, []string{"422", "429"}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			responses := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method, "responses")
			for _, status := range tc.present {
				if responses[status] == nil {
					t.Errorf("missing %s", status)
				}
			}
			for _, status := range tc.absent {
				if responses[status] != nil {
					t.Errorf("documents unreachable %s", status)
				}
			}
		})
	}
}

// Compare the mirrored public properties to the actual private handler types.
// A new response field in a handler must invalidate the document immediately.
func TestBloemAdministrationMirrorsPrivateWireRecords(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct{ file, handlerType, schema string }{
		{"access_groups.go", "accessGroupResponse", "BloemOrganizationGroup"},
		{"bloem_admin_session.go", "adminContextSessionRequest", "BloemAdminSessionRequest"},
		{"bloem_admin_session.go", "adminContextSessionResponse", "BloemAdminSessionResponse"},
		{"bloem_admin_session.go", "adminContextSummary", "BloemAdminContextSummary"},
		{"entitlement_templates.go", "entitlementPolicyJSON", "BloemEntitlementPolicy"},
		{"entitlement_templates.go", "entitlementTemplateJSON", "BloemEntitlementTemplate"},
		{"entitlement_templates.go", "entitlementApplyRequest", "BloemEntitlementApplyRequest"},
		{"entitlement_templates.go", "entitlementApplyJSON", "BloemEntitlementApplyResponse"},
		{"entitlement_bulk.go", "platformEntitlementCohort", "BloemEntitlementCohort"},
	} {
		t.Run(tc.handlerType, func(t *testing.T) {
			source, err := parser.ParseFile(token.NewFileSet(), "../api/handlers/"+tc.file, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var fields []string
			ast.Inspect(source, func(node ast.Node) bool {
				spec, ok := node.(*ast.TypeSpec)
				if !ok || spec.Name.Name != tc.handlerType {
					return true
				}
				record, ok := spec.Type.(*ast.StructType)
				if !ok {
					t.Fatal("handler record is not a struct")
				}
				for _, field := range record.Fields.List {
					if field.Tag == nil {
						continue
					}
					raw, err := strconv.Unquote(field.Tag.Value)
					if err != nil {
						t.Fatal(err)
					}
					name := strings.Split(reflect.StructTag(raw).Get("json"), ",")[0]
					if name != "" && name != "-" {
						fields = append(fields, name)
						bloemAssertPrivateWireType(t, doc, bloemDocObject(t, doc, "components", "schemas", tc.schema, "properties", name), field.Type)
					}
				}
				return false
			})
			if len(fields) == 0 {
				t.Fatal("private handler record not found")
			}
			bloemDocFields(t, bloemDocObject(t, doc, "components", "schemas", tc.schema), fields...)
		})
	}
}

func TestBloemLiveTVDeliveryDocumentUsesMediaAndTicketAuthority(t *testing.T) {
	doc := bloemWebDocument(t)
	streamPath := BloemPrefix + "/livetv/sessions/{sessionId}/stream"
	stream := bloemDocObject(t, doc, "paths", streamPath, "get")
	content := bloemDocObject(t, stream, "responses", "200", "content")
	if len(content) != 1 || content["video/mp2t"] == nil {
		t.Errorf("stream media = %v", content)
	}
	head := bloemDocObject(t, doc, "paths", streamPath, "head", "responses", "200")
	if head["content"] != nil {
		t.Error("HEAD must not advertise a response body")
	}
	hls := bloemDocObject(t, doc, "paths", BloemPrefix+"/livetv/live-hls/{playbackId}/{name}", "get")
	want := []any{map[string]any{"bloemLiveTVDeliveryTicket": []any{}}}
	if !reflect.DeepEqual(hls["security"], want) {
		t.Errorf("HLS authority = %v", hls["security"])
	}
	hlsContent := bloemDocObject(t, hls, "responses", "200", "content")
	if hlsContent["application/vnd.apple.mpegurl"] == nil || hlsContent["video/mp2t"] == nil || hlsContent["application/json"] != nil {
		t.Errorf("HLS media = %v", hlsContent)
	}
	heartbeat := bloemDocObject(t, doc, "paths", BloemPrefix+"/livetv/sessions/{sessionId}/heartbeat", "post")
	if heartbeat["requestBody"] != nil {
		t.Error("heartbeat does not decode JSON")
	}
	if _, ok := bloemDocObject(t, heartbeat, "responses")["204"]; !ok {
		t.Error("heartbeat success must be 204")
	}
}

func TestBloemNativeRequestSchemasAcceptHandlerDefaults(t *testing.T) {
	doc := bloemWebDocument(t)
	compiler := jsonschema.NewCompiler()
	const resource = "https://schema.example.invalid/bloem-native-defaults.json"
	if err := compiler.AddResource(resource, doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		value        map[string]any
	}{
		{"post", "/admin/organization/people/selections", map[string]any{}},
		{"post", "/admin/organization/groups/", map[string]any{"name": "Guests", "expected_revision": float64(7)}},
		{"post", "/admin/organization/groups/", map[string]any{"name": "Guests", "expected_revision": float64(7), "library_ids": nil, "allowed_permissions": nil}},
		{"put", "/admin/organization/groups/{id}", map[string]any{"expected_revision": float64(7), "library_ids": nil, "allowed_permissions": nil}},
		{"post", "/admin/platform/entitlement-templates", map[string]any{"key": "browse-only", "name": "Browse only", "policy": map[string]any{}}},
		{"post", "/admin/platform/entitlement-templates", map[string]any{"key": "browse-only", "name": "Browse only"}},
		{"post", "/admin/platform/entitlement-templates/{key}/revisions", map[string]any{"name": "Browse only", "expected_revision": float64(1), "policy": map[string]any{}}},
		{"post", "/admin/platform/accounts/{account_id}/entitlement/dry-run", map[string]any{"template_key": "browse-only", "template_revision": float64(1)}},
		{"post", "/livetv/recordings", map[string]any{"program_id": "program"}},
		{"post", "/livetv/series-rules", map[string]any{"channel_id": "channel"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			schema := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method, "requestBody", "content", "application/json", "schema")
			compiled, err := compiler.Compile(resource + schema["$ref"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(tc.value); err != nil {
				t.Errorf("valid handler request rejected: %v", err)
			}
		})
	}
}

// Native policy projections preserve null as an unrestricted/inherited set.
// The v2 default that arrays are always nonnull must not change this contract.
func TestBloemAdministrationPolicyNullability(t *testing.T) {
	doc := bloemWebDocument(t)
	compiler := jsonschema.NewCompiler()
	const resource = "https://schema.example.invalid/bloem-policy-wire.json"
	if err := compiler.AddResource(resource, doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		schema string
		body   any
	}{
		{"BloemOrganizationGroup", BloemOrganizationGroup{}},
		{"BloemEntitlementPolicy", BloemEntitlementPolicy{}},
		{"BloemOrganizationEntitlementRead", BloemOrganizationEntitlementRead{}},
		{"BloemLegacyAccountEntitlementRead", BloemLegacyAccountEntitlementRead{}},
		{"PolicyView", adminpeople.PolicyView{}},
		{"EffectivePolicySnapshot", entitlements.EffectivePolicySnapshot{}},
	} {
		t.Run(tc.schema, func(t *testing.T) {
			schema, err := compiler.Compile(resource + "#/components/schemas/" + tc.schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(bloemEngagementJSON(t, tc.body)); err != nil {
				t.Errorf("serialized native policy rejected: %v", err)
			}
		})
	}
}

// Check field kinds as well as names for private records. Nested records have
// their own cases above; exported policy views use the ordinary domain checker.
func bloemAssertPrivateWireType(t *testing.T, doc, schema map[string]any, expr ast.Expr) {
	t.Helper()
	if pointer, ok := expr.(*ast.StarExpr); ok {
		for _, variant := range anySlice(schema["anyOf"]) {
			candidate := variant.(map[string]any)
			if candidate["type"] != "null" {
				schema = candidate
				break
			}
		}
		bloemAssertPrivateWireType(t, doc, schema, pointer.X)
		return
	}
	schema = bloemDocSchema(t, doc, schema)
	var typ reflect.Type
	switch wire := expr.(type) {
	case *ast.Ident:
		switch wire.Name {
		case "string":
			typ = reflect.TypeFor[string]()
		case "bool":
			typ = reflect.TypeFor[bool]()
		case "int":
			typ = reflect.TypeFor[int]()
		case "int64":
			typ = reflect.TypeFor[int64]()
		default:
			return // Private nested records are compared by their own table row.
		}
	case *ast.ArrayType:
		if wire.Len != nil {
			t.Fatal("unhandled fixed wire array")
		}
		bloemAssertPrivateWireType(t, doc, bloemDocObject(t, schema, "items"), wire.Elt)
		if actual := schema["type"]; actual != "array" && !reflect.DeepEqual(actual, []any{"array", "null"}) {
			t.Errorf("array schema type=%v", actual)
		}
		return
	case *ast.SelectorExpr:
		switch wire.Sel.Name {
		case "Time", "UUID", "AdminScope":
			typ = reflect.TypeFor[string]()
		case "PolicyView":
			typ = reflect.TypeFor[adminpeople.PolicyView]()
		default:
			t.Fatalf("unhandled imported wire type %s", wire.Sel.Name)
		}
	default:
		t.Fatalf("unhandled private wire type %T", expr)
	}
	bloemDocMatchesType(t, doc, schema, typ)
}

func TestBloemLiveTVResponseSchemasValidateActualSerialization(t *testing.T) {
	doc := bloemWebDocument(t)
	compiler := jsonschema.NewCompiler()
	const resource = "https://schema.example.invalid/bloem-live-wire.json"
	if err := compiler.AddResource(resource, doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, status string
		body                 any
	}{
		{"delete", "/livetv/sessions/{sessionId}", "200", livetv.LiveSession{ID: "session", HLSURL: "https://private.example.invalid/tuner", StreamURL: "https://private.example.invalid/upstream", Status: "released"}},
		{"patch", "/livetv/channels/{channelId}", "200", livetv.Channel{ID: "channel", StreamURL: "https://private.example.invalid/upstream"}},
		{"post", "/livetv/tuners/{tunerId}/scan", "200", livetv.Tuner{ID: "tuner"}},
		{"post", "/livetv/tuners/{tunerId}/scan", "200", map[string]string{"id": "tuner", "status": "scanned"}},
		{"post", "/livetv/guide-sources/{sourceId}/sync", "200", map[string]string{"id": "source", "status": "ready"}},
		{"post", "/livetv/guide-sources/schedules-direct/lineups", "200", struct {
			Lineups []schedulesdirect.LineupOption `json:"lineups"`
		}{[]schedulesdirect.LineupOption{{Lineup: "lineup"}}}},
		{"post", "/livetv/guide-sources/xml-sync/lineups", "200", struct {
			Lineups []gracenote.LineupOption `json:"lineups"`
		}{[]gracenote.LineupOption{{Lineup: "lineup", Device: "device"}}}},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			schema := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method, "responses", tc.status, "content", "application/json", "schema")
			var compiled *jsonschema.Schema
			var err error
			if ref, ok := schema["$ref"].(string); ok {
				compiled, err = compiler.Compile(resource + ref)
			} else {
				// Union response schemas are inline, so compile their document location.
				pointer := strings.NewReplacer("~", "~0", "/", "~1").Replace(BloemPrefix + tc.path)
				compiled, err = compiler.Compile(resource + "#/paths/" + pointer + "/" + tc.method + "/responses/" + tc.status + "/content/application~1json/schema")
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "private.example.invalid") {
				t.Fatal("actual Live TV serializer disclosed a private upstream URL")
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(value); err != nil {
				t.Errorf("actual handler JSON rejected: %v", err)
			}
		})
	}
}
