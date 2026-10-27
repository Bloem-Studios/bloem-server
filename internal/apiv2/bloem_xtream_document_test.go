package apiv2

import "testing"

func TestBloemXtreamDocumentKeepsCredentialsWriteOnly(t *testing.T) {
	doc := bloemWebDocument(t)
	op := bloemDocObject(t, doc, "paths", BloemPrefix+"/livetv/tuners/xtream", "post")
	body := bloemDocSchema(t, doc, bloemDocObject(t, op, "requestBody", "content", "application/json", "schema"))
	for _, key := range []string{"username", "password"} {
		field := bloemDocObject(t, body, "properties", key)
		if field["writeOnly"] != true || field["type"] != "string" {
			t.Fatalf("%s is not a write-only credential", key)
		}
	}
	output := bloemDocSchema(t, doc, bloemDocObject(t, op, "responses", "201", "content", "application/json", "schema"))
	properties := bloemDocObject(t, output, "properties")
	for _, key := range []string{"username", "password", "credentials", "stream_url"} {
		if _, ok := properties[key]; ok {
			t.Fatalf("provider output contains %q", key)
		}
	}
	for _, status := range []string{"400", "401", "403", "404", "409", "500", "503"} {
		schema := bloemDocSchema(t, doc, bloemDocObject(t, op, "responses", status, "content", "application/json", "schema"))
		bloemDocFields(t, schema, "error", "code", "message")
	}
	// No Live TV handler can return not_implemented, and the provider route
	// has no upstream proxy to answer 502.
	for _, status := range []string{"501", "502"} {
		if _, ok := bloemDocObject(t, op, "responses")[status]; ok {
			t.Errorf("provider creation advertises unreachable %s", status)
		}
	}
	profileHeaders := 0
	for _, parameter := range anySlice(op["parameters"]) {
		if parameter.(map[string]any)["name"] == "X-Profile-Id" {
			profileHeaders++
		}
	}
	if profileHeaders != 1 {
		t.Errorf("X-Profile-Id declared %d times", profileHeaders)
	}
	capability := bloemDocObject(t, doc, "paths", BloemPrefix+"/livetv/capability", "get")
	shape := bloemDocSchema(t, doc, bloemDocObject(t, capability, "responses", "200", "content", "application/json", "schema"))
	if bloemDocObject(t, shape, "properties", "xtream_supported")["type"] != "boolean" {
		t.Fatal("capability does not advertise the installed Xtream protocol")
	}
}
