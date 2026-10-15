package apiv2

import "testing"

func TestBloemPreviewPublicDocument(t *testing.T) {
	doc := bloemWebDocument(t)
	if security, ok := doc["security"].([]any); ok && len(security) != 0 {
		t.Fatal("public preview operations inherit global credentials")
	}
	for _, tc := range []struct{ method, path, status string }{
		{"post", "/preview/sessions", "201"},
		{"get", "/preview/status", "200"},
	} {
		op := bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
		if security, ok := op["security"].([]any); ok && len(security) != 0 {
			t.Fatalf("public %s %s requires credentials: %v", tc.method, tc.path, op["security"])
		}
		bloemDocObject(t, op, "responses", tc.status, "content", "application/json", "schema")
	}
	session := bloemDocObject(t, doc, "components", "schemas", "BloemPreviewSession")
	bloemDocFields(t, session, "access_token", "refresh_token", "token_type", "expires_in", "expires_at", "item", "ref")
	status := bloemDocObject(t, doc, "components", "schemas", "BloemPreviewPublicStatus")
	bloemDocFields(t, status, "enabled", "brand", "storefront_url", "launch_url", "cta_url", "cta_label")
	if required, ok := status["required"].([]any); !ok || len(required) != 1 || required[0] != "enabled" {
		t.Fatalf("status requires host-specific presentation: %v", status["required"])
	}
}
