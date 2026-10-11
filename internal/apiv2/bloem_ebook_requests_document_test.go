package apiv2

import "testing"

func TestBloemEbookRequestDocumentRoutesAndOpaqueIntegrationIdentity(t *testing.T) {
	doc := bloemWebDocument(t)
	for _, tc := range []struct{ path, method string }{
		{"/ebook-requests/capability", "get"}, {"/ebook-requests/mine", "get"}, {"/ebook-requests", "post"}, {"/ebook-requests/{id}", "get"}, {"/ebook-requests/{id}/cancel", "post"},
		{"/admin/organization/ebook-requests", "get"}, {"/admin/organization/ebook-requests/{id}/approve", "post"}, {"/admin/organization/ebook-requests/{id}/decline", "post"}, {"/admin/organization/ebook-requests/{id}/retry", "post"},
		{"/admin/organization/ebook-request-integrations", "get"}, {"/admin/organization/ebook-request-integrations", "post"}, {"/admin/organization/ebook-request-integrations/{id}/library", "put"}, {"/admin/organization/ebook-request-policy", "put"},
	} {
		bloemDocObject(t, doc, "paths", BloemPrefix+tc.path, tc.method)
	}
	op := bloemDocObject(t, doc, "paths", BloemPrefix+"/admin/organization/ebook-request-integrations/{id}/library", "put")
	for _, v := range op["parameters"].([]any) {
		p := v.(map[string]any)
		if p["name"] == "id" {
			schema := p["schema"].(map[string]any)
			if schema["type"] != "string" || schema["format"] == "uuid" {
				t.Fatalf("integration IDs must be opaque strings: %+v", schema)
			}
		}
	}
	create := bloemDocObject(t, doc, "paths", BloemPrefix+"/ebook-requests", "post", "responses")
	if create["200"] == nil || create["201"] == nil {
		t.Fatal("durable idempotent replay response missing")
	}
}
