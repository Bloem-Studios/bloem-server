// Package compatv1 carries the private Compatibility Service API contract.
//
// The structural tests below are the contract's own gate: every operation must
// declare its capability, its stable error surface, and — when it mutates —
// its idempotency requirement, before any handler is allowed to implement it.
package compatv1

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// knownCapabilities is the reviewed capability vocabulary. It must stay in
// lockstep with internal/compatapi. "service" is the implicit base capability
// every enrolled application holds; the rest are granted per enrollment.
var knownCapabilities = map[string]bool{
	"service":  true,
	"identity": true,
	"catalog":  true,
	"state":    true,
	"playback": true,
	"livetv":   true,
	"events":   true,
}

// forbiddenSubjectParams are caller-selected subject identifiers. Subject
// identity comes only from the audience-bound subject token, never from a
// path, query, or header parameter an application chooses.
var forbiddenSubjectParams = map[string]bool{
	"account_id":      true,
	"accountid":       true,
	"user_id":         true,
	"userid":          true,
	"profile_id":      true,
	"profileid":       true,
	"organization_id": true,
	"membership_id":   true,
	"subject_id":      true,
}

type document struct {
	root map[string]any
}

type operation struct {
	Method string
	Path   string
	raw    map[string]any
	shared []any // path-level parameters
	doc    *document
}

func loadDocument(t *testing.T) *document {
	t.Helper()
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatalf("the contract must exist: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("the contract must parse as YAML: %v", err)
	}
	return &document{root: root}
}

func loadOperations(t *testing.T) []operation {
	t.Helper()
	doc := loadDocument(t)
	paths, ok := doc.root["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatal("the contract must declare a non-empty paths object")
	}
	var ops []operation
	for path, rawItem := range paths {
		item, ok := rawItem.(map[string]any)
		if !ok {
			t.Fatalf("path %s must be a mapping", path)
		}
		shared, _ := item["parameters"].([]any)
		for _, method := range []string{"get", "put", "post", "delete", "patch", "head", "options"} {
			rawOp, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			ops = append(ops, operation{
				Method: strings.ToUpper(method),
				Path:   path,
				raw:    rawOp,
				shared: shared,
				doc:    doc,
			})
		}
	}
	if len(ops) == 0 {
		t.Fatal("the contract must declare operations")
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops
}

func (d *document) resolve(ref string) map[string]any {
	const prefix = "#/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	node := any(d.root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, prefix), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[part]
	}
	resolved, _ := node.(map[string]any)
	return resolved
}

// resolveNode follows one level of $ref indirection if present.
func (d *document) resolveNode(node any) map[string]any {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := m["$ref"].(string); ok {
		return d.resolve(ref)
	}
	return m
}

func (op operation) ID() string {
	id, _ := op.raw["operationId"].(string)
	return id
}

func (op operation) Extension(name string) string {
	value, _ := op.raw[name].(string)
	return value
}

func (op operation) Mutates() bool {
	switch op.Method {
	case "GET", "HEAD", "OPTIONS":
		return false
	}
	return true
}

func (op operation) parameters() []map[string]any {
	var out []map[string]any
	for _, raw := range append(append([]any{}, op.shared...), listOf(op.raw["parameters"])...) {
		if p := op.doc.resolveNode(raw); p != nil {
			out = append(out, p)
		}
	}
	return out
}

func listOf(v any) []any {
	list, _ := v.([]any)
	return list
}

func (op operation) RequiresHeader(name string) bool {
	for _, p := range op.parameters() {
		in, _ := p["in"].(string)
		pname, _ := p["name"].(string)
		required, _ := p["required"].(bool)
		if in == "header" && strings.EqualFold(pname, name) && required {
			return true
		}
	}
	return false
}

func (op operation) hasQueryParam(name string) bool {
	for _, p := range op.parameters() {
		in, _ := p["in"].(string)
		pname, _ := p["name"].(string)
		if in == "query" && pname == name {
			return true
		}
	}
	return false
}

func requireResponses(t *testing.T, op operation, codes ...int) {
	t.Helper()
	responses, ok := op.raw["responses"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s must declare responses", op.Method, op.Path)
	}
	for _, code := range codes {
		if _, ok := responses[fmt.Sprintf("%d", code)]; !ok {
			t.Errorf("%s %s must declare a %d response", op.Method, op.Path, code)
		}
	}
}

func TestContractIsOpenAPI31(t *testing.T) {
	doc := loadDocument(t)
	version, _ := doc.root["openapi"].(string)
	if !strings.HasPrefix(version, "3.1") {
		t.Fatalf("contract must be OpenAPI 3.1, got %q", version)
	}
	info, _ := doc.root["info"].(map[string]any)
	if info == nil || info["version"] == nil || info["title"] == nil {
		t.Fatal("contract must declare info.title and info.version")
	}
}

func TestOperationsDeclareCapabilityIdempotencyAndErrors(t *testing.T) {
	for _, op := range loadOperations(t) {
		if op.Extension("x-vondel-capability") == "" {
			t.Errorf("%s %s must declare x-vondel-capability", op.Method, op.Path)
		}
		requireResponses(t, op, 401, 403, 409, 429, 503)
		if op.Mutates() && !op.RequiresHeader("Idempotency-Key") {
			t.Errorf("%s %s mutates and must require the Idempotency-Key header", op.Method, op.Path)
		}
	}
}

func TestOperationCapabilitiesAreKnown(t *testing.T) {
	for _, op := range loadOperations(t) {
		if capability := op.Extension("x-vondel-capability"); !knownCapabilities[capability] {
			t.Errorf("%s %s declares unknown capability %q", op.Method, op.Path, capability)
		}
	}
}

func TestOperationIDsAreUniqueAndPresent(t *testing.T) {
	seen := map[string]string{}
	for _, op := range loadOperations(t) {
		id := op.ID()
		if id == "" {
			t.Errorf("%s %s must declare operationId", op.Method, op.Path)
			continue
		}
		if prev, dup := seen[id]; dup {
			t.Errorf("operationId %q is declared by both %s and %s %s", id, prev, op.Method, op.Path)
		}
		seen[id] = op.Method + " " + op.Path
	}
}

// TestNoCallerSelectedSubjectParameters proves an application can never name
// the subject it acts for: subject identity travels only inside the signed,
// audience-bound subject token.
func TestNoCallerSelectedSubjectParameters(t *testing.T) {
	for _, op := range loadOperations(t) {
		for _, p := range op.parameters() {
			name, _ := p["name"].(string)
			if forbiddenSubjectParams[strings.ToLower(name)] {
				t.Errorf("%s %s declares caller-selected subject parameter %q", op.Method, op.Path, name)
			}
		}
	}
}

// TestListOperationsUseSignedCursors requires every operation marked as a list
// to take the signed cursor and bounded limit parameters.
func TestListOperationsUseSignedCursors(t *testing.T) {
	found := 0
	for _, op := range loadOperations(t) {
		if list, _ := op.raw["x-vondel-list"].(bool); !list {
			continue
		}
		found++
		if !op.hasQueryParam("cursor") || !op.hasQueryParam("limit") {
			t.Errorf("%s %s is a list operation and must declare cursor and limit query parameters", op.Method, op.Path)
		}
	}
	if found == 0 {
		t.Fatal("the contract must mark its list operations with x-vondel-list")
	}
}

// TestErrorResponsesShareTheStableEnvelope pins the canonical error components
// to one ErrorEnvelope schema so every failure a companion sees decodes the
// same way.
func TestErrorResponsesShareTheStableEnvelope(t *testing.T) {
	doc := loadDocument(t)
	for _, name := range []string{"BadRequest", "Unauthorized", "Forbidden", "NotFound", "Conflict", "RateLimited", "Unavailable"} {
		resp := doc.resolve("#/components/responses/" + name)
		if resp == nil {
			t.Errorf("components.responses.%s must exist", name)
			continue
		}
		content, _ := resp["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		ref, _ := schema["$ref"].(string)
		if ref != "#/components/schemas/ErrorEnvelope" {
			t.Errorf("components.responses.%s must use the ErrorEnvelope schema, got %q", name, ref)
		}
	}
	envelope := doc.resolve("#/components/schemas/ErrorEnvelope")
	if envelope == nil {
		t.Fatal("components.schemas.ErrorEnvelope must exist")
	}
}

// TestSecurityIsBearerEverywhereExceptEnrollment: enrollment authenticates
// with the one-use secret in its body; everything else requires the service
// bearer credential.
func TestSecurityIsBearerEverywhereExceptEnrollment(t *testing.T) {
	doc := loadDocument(t)
	schemes := doc.resolve("#/components/securitySchemes/serviceBearer")
	if schemes == nil {
		t.Fatal("components.securitySchemes.serviceBearer must exist")
	}
	for _, op := range loadOperations(t) {
		security, declared := op.raw["security"]
		if op.Path == "/enroll" {
			if list := listOf(security); !declared || len(list) != 0 {
				t.Errorf("POST /enroll must declare an empty security array, got %v", security)
			}
			continue
		}
		requiresBearer := false
		for _, entry := range listOf(security) {
			m, _ := entry.(map[string]any)
			if _, ok := m["serviceBearer"]; ok {
				requiresBearer = true
			}
		}
		if declared && !requiresBearer {
			t.Errorf("%s %s overrides security without requiring serviceBearer", op.Method, op.Path)
		}
		if !declared {
			// Falls back to the document default, which must require it.
			defaults := listOf(doc.root["security"])
			ok := false
			for _, entry := range defaults {
				m, _ := entry.(map[string]any)
				if _, has := m["serviceBearer"]; has {
					ok = true
				}
			}
			if !ok {
				t.Fatalf("document-level security must require serviceBearer for %s %s", op.Method, op.Path)
			}
		}
	}
}

// TestSubjectOperationsRequireTheSubjectTokenHeader: operations marked
// subject-scoped resolve their subject from the X-Vondel-Subject-Token
// header and must declare it required.
func TestSubjectOperationsRequireTheSubjectTokenHeader(t *testing.T) {
	found := 0
	for _, op := range loadOperations(t) {
		subjectScoped, _ := op.raw["x-vondel-subject"].(bool)
		if !subjectScoped {
			continue
		}
		found++
		if !op.RequiresHeader("X-Vondel-Subject-Token") {
			t.Errorf("%s %s is subject-scoped and must require X-Vondel-Subject-Token", op.Method, op.Path)
		}
	}
	if found == 0 {
		t.Fatal("the contract must mark its subject-scoped operations with x-vondel-subject")
	}
}
